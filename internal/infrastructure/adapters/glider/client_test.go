package glider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc, cfg Config) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg.BaseURL = server.URL
	if cfg.APIKey == "" {
		cfg.APIKey = "test-key"
	}
	return NewClient(cfg, nil), server
}

func TestClientSendsCredentialsAndDecodesEnvelope(t *testing.T) {
	var seenKey, seenCorrelation, seenAccept string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("x-api-key")
		seenCorrelation = r.Header.Get("X-Correlation-Id")
		seenAccept = r.Header.Get("Accept")
		assert.Equal(t, "/whoami", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"data":{"apiKeyId":"key-1","tenantName":"tenant","scopes":["read","enroll"]}}`))
	}, Config{CorrelationID: "corr-fixed"})

	identity, err := client.Whoami(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "key-1", identity.APIKeyID)
	assert.Equal(t, "tenant", identity.TenantName)
	assert.Equal(t, []string{"read", "enroll"}, identity.Scopes)
	assert.Equal(t, "test-key", seenKey)
	assert.Equal(t, "corr-fixed", seenCorrelation)
	assert.Equal(t, "application/json", seenAccept)
}

func TestClientSurfacesCursorAndNestedData(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"portfolios":[{"portfolioId":"p1"}]},"nextCursor":"cur-2"}`))
	}, Config{})

	portfolios, err := client.ListPortfolios(context.Background())
	require.NoError(t, err)
	require.Len(t, portfolios, 1)
	assert.Equal(t, "p1", portfolios[0].PortfolioID)
}

func TestClientWithoutAPIKeyFailsClosed(t *testing.T) {
	var calls int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}, Config{APIKey: " "})

	_, err := client.Whoami(context.Background())
	apiErr, ok := AsAPIError(err)
	require.True(t, ok, "expected a structured API error, got %v", err)
	assert.True(t, apiErr.IsUnauthorized())
	assert.Contains(t, apiErr.Message, "api key is not configured")
	assert.Zero(t, atomic.LoadInt64(&calls), "an unconfigured key must not reach the network")
}

func TestClientMapsEnvelopeErrorWithCode(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"INVALID_ALLOCATION","message":"weights must sum to 100","details":["leg 1"]}}`))
	}, Config{})

	_, err := client.GetStrategy(context.Background(), "s1")
	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.True(t, apiErr.IsValidation())
	assert.Equal(t, "INVALID_ALLOCATION", apiErr.Code)
	assert.Equal(t, []string{"leg 1"}, apiErr.Details)
	assert.NotEmpty(t, apiErr.CorrelationID)
	assert.False(t, apiErr.IsRetryable(), "a 400 must never be retried")
}

func TestClientMapsCooldownWithRetryAfter(t *testing.T) {
	attempts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"REBALANCE_COOLDOWN","message":"too soon"}}`))
	}, Config{MaxRetries: 1})

	_, err := client.GetPortfolio(context.Background(), "p1")
	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.True(t, apiErr.IsCooldown())
	assert.Equal(t, "REBALANCE_COOLDOWN", apiErr.Code)
	assert.Equal(t, 2, attempts, "a cooldown on a read is retried once")
}

func TestClientRetriesOnlyIdempotentReads(t *testing.T) {
	var reads, writes int64
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			atomic.AddInt64(&reads, 1)
		} else {
			atomic.AddInt64(&writes, 1)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"UPSTREAM","message":"boom"}}`))
	}, Config{MaxRetries: 2})

	_, err := client.GetStrategy(context.Background(), "s1")
	require.Error(t, err)
	assert.Equal(t, int64(3), atomic.LoadInt64(&reads), "a read uses every allowed attempt")

	// An unanchored write must be attempted exactly once: we cannot know whether
	// the provider applied it.
	_, err = client.CreateStrategy(context.Background(), entities.GliderStrategyInput{})
	require.Error(t, err)
	assert.Equal(t, int64(1), atomic.LoadInt64(&writes))
}

func TestClientMapsNonJSONErrorBody(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}, Config{})

	_, err := client.Whoami(context.Background())
	apiErr, ok := AsAPIError(err)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Message, "502 Bad Gateway")
	assert.True(t, apiErr.IsRetryable())
}

func TestClientMapsTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close() // nothing is listening any more

	client := NewClient(Config{BaseURL: baseURL, APIKey: "test-key", Timeout: 200 * time.Millisecond}, nil)
	_, err := client.Whoami(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProviderUnavailable)
	assert.True(t, IsRetryableError(err))
}

func TestClientHonoursContextCancellationDuringBackoff(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, Config{MaxRetries: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Whoami(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second, "cancellation must stop the retry loop")
}

func TestClientRejectsUndecodableSuccessBody(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"apiKeyId":42}}`))
	}, Config{})

	_, err := client.Whoami(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode data")
	assert.False(t, IsRetryableError(err))
}

func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, 3*time.Second, parseRetryAfter("3"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("0"))
	assert.Equal(t, time.Duration(0), parseRetryAfter("-5"))
	assert.Equal(t, time.Duration(0), parseRetryAfter(""))
	assert.Equal(t, time.Duration(0), parseRetryAfter("not-a-date"))

	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	parsed := parseRetryAfter(future)
	assert.Greater(t, parsed, 20*time.Second)
	assert.LessOrEqual(t, parsed, 30*time.Second)

	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	assert.Equal(t, time.Duration(0), parseRetryAfter(past))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.Equal(t, "abc...", truncate("abcdef", 3))
}

// The enrollment submit path is idempotent on flowId, so the client is allowed
// to replay it. This pins that contract: a retried submit sends the same body.
func TestClientReplaysIdempotentEnrollmentSubmit(t *testing.T) {
	var bodies []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buffer)
		bodies = append(bodies, string(buffer))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"portfolioId":"p1"}}`))
	}, Config{MaxRetries: 2})

	portfolio, err := client.SubmitEnrollment(context.Background(), entities.GliderEnrollSubmitInput{FlowID: "flow-1"})
	require.NoError(t, err)
	assert.Equal(t, "p1", portfolio.PortfolioID)
	require.Len(t, bodies, 2)
	assert.Equal(t, bodies[0], bodies[1], "a replay must use the identical idempotency anchor")
	assert.Contains(t, bodies[1], "flow-1")
}

func TestClientSetsContentLengthForBodies(t *testing.T) {
	var length int64 = -1
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		length = r.ContentLength
		_, _ = w.Write([]byte(`{"success":true,"data":{"portfolioId":"p1"}}`))
	}, Config{})

	_, err := client.TriggerRebalance(context.Background(), "p1")
	require.NoError(t, err)
	assert.Greater(t, length, int64(0), "a POST body must declare its length, got "+strconv.FormatInt(length, 10))
}

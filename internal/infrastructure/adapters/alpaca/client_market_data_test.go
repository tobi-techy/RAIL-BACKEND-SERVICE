package alpaca

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDoDataRequestReturnsTypedClientError(t *testing.T) {
	client := NewClient(Config{
		ClientID:    "test-key",
		SecretKey:   "test-secret",
		DataBaseURL: "https://data.example.test",
	}, zap.NewNop())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "test-key", r.Header.Get("APCA-API-KEY-ID"))
			assert.Equal(t, "test-secret", r.Header.Get("APCA-API-SECRET-KEY"))
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("<html><body>401 Authorization Required</body></html>")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	err := client.doDataRequest(context.Background(), http.MethodGet, "/v2/stocks/AAPL/quotes/latest", nil, nil)
	require.Error(t, err)

	var clientErr *ClientError
	require.ErrorAs(t, err, &clientErr)
	assert.Equal(t, http.StatusUnauthorized, clientErr.StatusCode)
}

func TestDoDataRequestReturnsTypedRateLimitError(t *testing.T) {
	client := NewClient(Config{
		ClientID:    "test-key",
		SecretKey:   "test-secret",
		DataBaseURL: "https://data.example.test",
	}, zap.NewNop())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"message":"rate limited","retry_after":"7"}`)),
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
			}, nil
		}),
	}

	err := client.doDataRequest(context.Background(), http.MethodGet, "/v2/stocks/AAPL/quotes/latest", nil, nil)
	require.Error(t, err)

	var rateLimitErr *RateLimitError
	require.ErrorAs(t, err, &rateLimitErr)
	assert.Equal(t, 7*time.Second, rateLimitErr.RetryAfter())
}

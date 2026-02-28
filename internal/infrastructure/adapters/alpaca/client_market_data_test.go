package alpaca

import (
	"context"
	"io"
	"net/http"
	"net/url"
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

func TestGetBarsFallsBackToAlternateFeed(t *testing.T) {
	client := NewClient(Config{
		ClientID:    "test-key",
		SecretKey:   "test-secret",
		DataBaseURL: "https://data.example.test",
		DataFeed:    "sip",
	}, zap.NewNop())

	callCount := 0
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			callCount++
			values, err := url.ParseQuery(r.URL.RawQuery)
			require.NoError(t, err)

			feed := values.Get("feed")
			switch callCount {
			case 1:
				assert.Equal(t, "sip", feed)
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{"message":"subscription does not permit querying recent SIP data"}`)),
					Header:     make(http.Header),
				}, nil
			case 2:
				assert.Equal(t, "iex", feed)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"bars":[
							{"o":178.00,"h":180.25,"l":177.80,"c":179.90,"v":1200000,"t":"2026-02-25T00:00:00Z"}
						]
					}`)),
					Header: make(http.Header),
				}, nil
			default:
				t.Fatalf("unexpected call count: %d", callCount)
				return nil, nil
			}
		}),
	}

	start := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	bars, err := client.GetBars(context.Background(), "AAPL", "1Day", start, end)
	require.NoError(t, err)
	require.Len(t, bars, 1)
	assert.Equal(t, "AAPL", bars[0].Symbol)
	assert.Equal(t, "179.9", bars[0].Close.String())
	assert.Equal(t, 2, callCount)
}

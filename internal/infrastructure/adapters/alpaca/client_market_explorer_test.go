package alpaca

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGetStockSnapshotParsesQuoteFields(t *testing.T) {
	client := NewClient(Config{
		ClientID:    "broker-key",
		SecretKey:   "broker-secret",
		DataBaseURL: "https://data.example.test",
	}, zap.NewNop())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/v2/stocks/AAPL/snapshot", r.URL.Path)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"latestTrade":{"p":101.5,"s":10,"t":"2026-02-25T15:00:00Z"},
					"latestQuote":{"ap":101.6,"bp":101.4,"as":2,"bs":3,"t":"2026-02-25T15:00:01Z"},
					"dailyBar":{"o":100.0,"h":102.0,"l":99.0,"c":101.5,"v":1234,"t":"2026-02-25T00:00:00Z"},
					"prevDailyBar":{"o":98.0,"h":99.0,"l":97.0,"c":98.5,"v":1100,"t":"2026-02-24T00:00:00Z"}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	}

	quote, err := client.GetStockSnapshot(context.Background(), "AAPL")
	require.NoError(t, err)
	require.NotNil(t, quote)

	assert.Equal(t, "AAPL", quote.Symbol)
	assert.Equal(t, "101.5", quote.Price.String())
	assert.Equal(t, "101.4", quote.Bid.String())
	assert.Equal(t, "101.6", quote.Ask.String())
	assert.Equal(t, int64(1234), quote.Volume)
	assert.Equal(t, "3", quote.Change.String())
	assert.InDelta(t, 3.045685279187817, quote.ChangePct.InexactFloat64(), 1e-12)
}

func TestGetStockSnapshotsParsesMultipleSymbols(t *testing.T) {
	client := NewClient(Config{
		ClientID:    "broker-key",
		SecretKey:   "broker-secret",
		DataBaseURL: "https://data.example.test",
	}, zap.NewNop())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/v2/stocks/snapshots", r.URL.Path)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"snapshots":{
						"AAPL":{"latestTrade":{"p":100,"t":"2026-02-25T15:00:00Z"},"latestQuote":{"ap":100.2,"bp":99.8,"t":"2026-02-25T15:00:01Z"},"dailyBar":{"o":99,"h":101,"l":98,"c":100,"v":1000},"prevDailyBar":{"c":98}},
						"MSFT":{"latestTrade":{"p":200,"t":"2026-02-25T15:00:00Z"},"latestQuote":{"ap":200.2,"bp":199.8,"t":"2026-02-25T15:00:01Z"},"dailyBar":{"o":198,"h":201,"l":197,"c":200,"v":2000},"prevDailyBar":{"c":199}}
					}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	}

	quotes, err := client.GetStockSnapshots(context.Background(), []string{"AAPL", "MSFT"})
	require.NoError(t, err)
	require.Len(t, quotes, 2)

	assert.Equal(t, "100", quotes["AAPL"].Price.String())
	assert.Equal(t, "200", quotes["MSFT"].Price.String())
}

func TestGetStockSnapshotReturnsTypedErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		check      func(t *testing.T, err error)
	}{
		{
			name:       "client_error",
			statusCode: http.StatusUnauthorized,
			body:       `{"message":"unauthorized"}`,
			check: func(t *testing.T, err error) {
				var target *ClientError
				require.True(t, errors.As(err, &target))
			},
		},
		{
			name:       "rate_limit",
			statusCode: http.StatusTooManyRequests,
			body:       `{"message":"rate limit"}`,
			check: func(t *testing.T, err error) {
				var target *RateLimitError
				require.True(t, errors.As(err, &target))
			},
		},
		{
			name:       "server_error",
			statusCode: http.StatusBadGateway,
			body:       `{"message":"upstream"}`,
			check: func(t *testing.T, err error) {
				var target *ServerError
				require.True(t, errors.As(err, &target))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(Config{
				ClientID:    "broker-key",
				SecretKey:   "broker-secret",
				DataBaseURL: "https://data.example.test",
			}, zap.NewNop())
			client.httpClient = &http.Client{
				Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(strings.NewReader(tt.body)),
						Header:     make(http.Header),
					}, nil
				}),
			}

			_, err := client.GetStockSnapshot(context.Background(), "AAPL")
			require.Error(t, err)
			tt.check(t, err)
		})
	}
}

func TestGetStockSnapshotUsesDedicatedDataCredentials(t *testing.T) {
	client := NewClient(Config{
		ClientID:      "broker-key",
		SecretKey:     "broker-secret",
		DataAPIKey:    "data-key",
		DataAPISecret: "data-secret",
		DataBaseURL:   "https://data.example.test",
	}, zap.NewNop())
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "data-key", r.Header.Get("APCA-API-KEY-ID"))
			assert.Equal(t, "data-secret", r.Header.Get("APCA-API-SECRET-KEY"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"latestTrade":{"p":100,"t":"2026-02-25T15:00:00Z"},
					"latestQuote":{"ap":100.2,"bp":99.8,"t":"2026-02-25T15:00:01Z"},
					"dailyBar":{"o":99,"h":101,"l":98,"c":100,"v":1000},
					"prevDailyBar":{"c":98}
				}`)),
				Header: make(http.Header),
			}, nil
		}),
	}

	_, err := client.GetStockSnapshot(context.Background(), "AAPL")
	require.NoError(t, err)
}

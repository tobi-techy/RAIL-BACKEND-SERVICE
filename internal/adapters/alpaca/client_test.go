package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestNewClient(t *testing.T) {
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name     string
		config   Config
		wantErr  bool
		validate func(t *testing.T, client *Client)
	}{
		{
			name: "default config",
			config: Config{
				APIKey:    "test-key",
				APISecret: "test-secret",
			},
			wantErr: false,
			validate: func(t *testing.T, client *Client) {
				assert.Equal(t, defaultTimeout, client.config.Timeout)
				assert.Equal(t, alpacaRateLimitRPM, client.config.RateLimitRPM)
				assert.Equal(t, rateLimitBurst, client.config.RateLimitBurst)
				assert.Contains(t, client.config.BaseURL, "sandbox.alpaca.markets")
			},
		},
		{
			name: "production config",
			config: Config{
				APIKey:      "test-key",
				APISecret:   "test-secret",
				Environment: "production",
			},
			wantErr: false,
			validate: func(t *testing.T, client *Client) {
				assert.Contains(t, client.config.BaseURL, "broker-api.alpaca.markets")
			},
		},
		{
			name: "custom config",
			config: Config{
				APIKey:         "test-key",
				APISecret:      "test-secret",
				BaseURL:        "https://custom.api.com",
				DataBaseURL:    "https://custom.data.com",
				Timeout:        10 * time.Second,
				RateLimitRPM:   100,
				RateLimitBurst: 10,
			},
			wantErr: false,
			validate: func(t *testing.T, client *Client) {
				assert.Equal(t, "https://custom.api.com", client.config.BaseURL)
				assert.Equal(t, "https://custom.data.com", client.config.DataBaseURL)
				assert.Equal(t, 10*time.Second, client.config.Timeout)
				assert.Equal(t, 100, client.config.RateLimitRPM)
				assert.Equal(t, 10, client.config.RateLimitBurst)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.config, logger)
			require.NotNil(t, client)
			if tt.validate != nil {
				tt.validate(t, client)
			}
			client.Close() // Clean up
		})
	}
}

func TestClient_GetAsset(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/assets/AAPL", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("APCA-API-KEY-ID"))
		assert.Equal(t, "test-api-secret", r.Header.Get("APCA-API-SECRET-KEY"))

		// Return mock response
		response := entities.AlpacaAssetResponse{
			ID:           "test-id",
			Symbol:       "AAPL",
			Name:         "Apple Inc",
			Status:       entities.AlpacaAssetStatusActive,
			Tradable:     true,
			Marginable:   true,
			Shortable:    true,
			EasyToBorrow: true,
			Fractionable: true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	asset, err := client.GetAsset(context.Background(), "AAPL")

	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "AAPL", asset.Symbol)
	assert.Equal(t, "Apple Inc", asset.Name)
	assert.True(t, asset.Tradable)
}

func TestClient_GetAsset_NotFound(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Mock server returning 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(entities.AlpacaErrorResponse{
			Code:    http.StatusNotFound,
			Message: "asset not found",
		})
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	asset, err := client.GetAsset(context.Background(), "INVALID")

	assert.Error(t, err)
	assert.Nil(t, asset)

	var apiErr *entities.AlpacaErrorResponse
	assert.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestClient_ListAssets(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/assets", r.URL.Path)
		assert.Equal(t, "active", r.URL.Query().Get("status"))
		assert.Equal(t, "true", r.URL.Query().Get("tradable"))

		// Return mock response
		response := []entities.AlpacaAssetResponse{
			{
				ID:       "1",
				Symbol:   "AAPL",
				Name:     "Apple Inc",
				Status:   entities.AlpacaAssetStatusActive,
				Tradable: true,
			},
			{
				ID:       "2",
				Symbol:   "GOOGL",
				Name:     "Alphabet Inc",
				Status:   entities.AlpacaAssetStatusActive,
				Tradable: true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	assets, err := client.ListAssets(context.Background(), map[string]string{
		"status":   "active",
		"tradable": "true",
	})

	require.NoError(t, err)
	assert.Len(t, assets, 2)
	assert.Equal(t, "AAPL", assets[0].Symbol)
	assert.Equal(t, "GOOGL", assets[1].Symbol)
}

func TestClient_RateLimiting(t *testing.T) {
	logger := zaptest.NewLogger(t)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		response := entities.AlpacaAssetResponse{
			ID:     "test-id",
			Symbol: "AAPL",
			Name:   "Apple Inc",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Client with very low rate limit for testing
	client := NewClient(Config{
		APIKey:         "test-api-key",
		APISecret:      "test-api-secret",
		BaseURL:        server.URL,
		Timeout:        5 * time.Second,
		RateLimitRPM:   60, // 1 request per second
		RateLimitBurst: 1,  // Only 1 burst request
	}, logger)
	defer client.Close()

	// First request should succeed
	_, err1 := client.GetAsset(context.Background(), "AAPL")

	// Second request should be rate limited
	start := time.Now()
	_, err2 := client.GetAsset(context.Background(), "AAPL")
	elapsed2 := time.Since(start)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.Equal(t, 2, callCount)

	// Second request should have taken at least 1 second due to rate limiting
	assert.True(t, elapsed2 >= time.Second, "Second request should be rate limited")
}

func TestClient_RetryLogic(t *testing.T) {
	logger := zaptest.NewLogger(t)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			// Fail first 2 attempts with 500 error
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(entities.AlpacaErrorResponse{
				Code:    http.StatusInternalServerError,
				Message: "Internal server error",
			})
			return
		}

		// Succeed on third attempt
		response := entities.AlpacaAssetResponse{
			ID:     "test-id",
			Symbol: "AAPL",
			Name:   "Apple Inc",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	asset, err := client.GetAsset(context.Background(), "AAPL")

	assert.NoError(t, err)
	assert.NotNil(t, asset)
	assert.Equal(t, 3, callCount) // Should have retried 3 times
	assert.Equal(t, "AAPL", asset.Symbol)
}

func TestClient_RateLimitRetry(t *testing.T) {
	logger := zaptest.NewLogger(t)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call gets rate limited
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(entities.AlpacaErrorResponse{
				Code:    http.StatusTooManyRequests,
				Message: "Rate limit exceeded",
			})
			return
		}

		// Second call succeeds
		response := entities.AlpacaAssetResponse{
			ID:     "test-id",
			Symbol: "AAPL",
			Name:   "Apple Inc",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	start := time.Now()
	asset, err := client.GetAsset(context.Background(), "AAPL")
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.NotNil(t, asset)
	assert.Equal(t, 2, callCount)
	// Rate limiting worked - we made 2 calls and it took some time
	assert.True(t, elapsed >= 500*time.Millisecond, "Should have waited for retry backoff")
	assert.Equal(t, "AAPL", asset.Symbol)
}

func TestClient_Timeout(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Server that never responds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Longer than client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   100 * time.Millisecond, // Very short timeout
	}, logger)
	defer client.Close()

	_, err := client.GetAsset(context.Background(), "AAPL")

	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "timeout")
}

func TestClient_ContextCancellation(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Server that takes time to respond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		response := entities.AlpacaAssetResponse{
			ID:     "test-id",
			Symbol: "AAPL",
			Name:   "Apple Inc",
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)
	defer client.Close()

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetAsset(ctx, "AAPL")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "rate limit error",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusTooManyRequests,
			},
			expected: true,
		},
		{
			name: "server error 500",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusInternalServerError,
			},
			expected: true,
		},
		{
			name: "server error 502",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusBadGateway,
			},
			expected: true,
		},
		{
			name: "server error 503",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusServiceUnavailable,
			},
			expected: true,
		},
		{
			name: "server error 504",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusGatewayTimeout,
			},
			expected: true,
		},
		{
			name: "client error 400",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusBadRequest,
			},
			expected: false,
		},
		{
			name: "client error 404",
			err: &entities.AlpacaErrorResponse{
				Code: http.StatusNotFound,
			},
			expected: false,
		},
		{
			name:     "network timeout error",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "connection refused",
			err:      &url.Error{Err: &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		minTime time.Duration
		maxTime time.Duration
	}{
		{1, 500 * time.Millisecond, 2 * time.Second},
		{2, 1 * time.Second, 4 * time.Second},
		{3, 2 * time.Second, 8 * time.Second},
		{4, 4 * time.Second, 16 * time.Second},
		{5, 8 * time.Second, 16 * time.Second}, // Should cap at maxBackoff
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			// Run multiple times to account for jitter
			for i := 0; i < 10; i++ {
				result := calculateBackoff(tt.attempt)
				assert.True(t, result >= tt.minTime, "Backoff too short: %v", result)
				assert.True(t, result <= tt.maxTime, "Backoff too long: %v", result)
			}
		})
	}
}

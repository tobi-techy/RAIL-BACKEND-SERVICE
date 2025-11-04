package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/internal/adapters/alpaca"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupAlpacaTest(t *testing.T) (*AlpacaHandlers, *httptest.Server) {
	logger := zaptest.NewLogger(t)

	// Create mock Alpaca server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock asset response
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

	// Create Alpaca client pointing to mock server
	alpacaClient := alpaca.NewClient(alpaca.Config{
		APIKey:    "test-key",
		APISecret: "test-secret",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}, logger)

	// Create handlers
	handlers := NewAlpacaHandlers(alpacaClient, logger)

	return handlers, server
}

func TestAlpacaHandlers_GetAsset(t *testing.T) {
	handlers, server := setupAlpacaTest(t)
	defer server.Close()

	// Create Gin router and register route
	router := gin.New()
	router.GET("/api/v1/assets/:symbol_or_id", handlers.GetAsset)

	tests := []struct {
		name           string
		symbol         string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "valid symbol",
			symbol:         "AAPL",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var asset entities.AlpacaAssetResponse
				err := json.Unmarshal(body, &asset)
				require.NoError(t, err)
				assert.Equal(t, "AAPL", asset.Symbol)
				assert.Equal(t, "Apple Inc", asset.Name)
				assert.True(t, asset.Tradable)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/api/v1/assets/"+tt.symbol, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

func TestAlpacaHandlers_GetAssets(t *testing.T) {
	handlers, server := setupAlpacaTest(t)
	defer server.Close()

	// Override the server handler to return multiple assets
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock multiple assets response
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
			{
				ID:       "3",
				Symbol:   "MSFT",
				Name:     "Microsoft Corporation",
				Status:   entities.AlpacaAssetStatusActive,
				Tradable: true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Create Gin router and register route
	router := gin.New()
	router.GET("/api/v1/assets", handlers.GetAssets)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "default parameters",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 3)
				assert.Equal(t, 3, response.TotalCount)
				assert.Equal(t, "AAPL", response.Assets[0].Symbol)
				assert.Equal(t, "GOOGL", response.Assets[1].Symbol)
			},
		},
		{
			name:           "with pagination",
			queryParams:    "?page=1&page_size=2",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 2)
				assert.Equal(t, 3, response.TotalCount)
				assert.Equal(t, 1, response.Page)
				assert.Equal(t, 2, response.PageSize)
			},
		},
		{
			name:           "with search",
			queryParams:    "?search=apple",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 1)
				assert.Equal(t, "AAPL", response.Assets[0].Symbol)
			},
		},
		{
			name:           "invalid page number",
			queryParams:    "?page=0",
			expectedStatus: http.StatusOK, // Should default to page 1
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Equal(t, 1, response.Page)
			},
		},
		{
			name:           "invalid boolean filter",
			queryParams:    "?tradable=maybe",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "INVALID_BOOLEAN_FILTER", errResp.Code)
			},
		},
		{
			name:           "invalid asset class",
			queryParams:    "?asset_class=crypto123",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "INVALID_ASSET_CLASS", errResp.Code)
			},
		},
		{
			name:           "invalid exchange",
			queryParams:    "?exchange=INVALID",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "INVALID_EXCHANGE", errResp.Code)
			},
		},
		{
			name:           "search too short",
			queryParams:    "?search=a",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "SEARCH_TOO_SHORT", errResp.Code)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/api/v1/assets"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

func TestAlpacaHandlers_SearchAssets(t *testing.T) {
	handlers, server := setupAlpacaTest(t)
	defer server.Close()

	// Override server to return assets for search
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := []entities.AlpacaAssetResponse{
			{ID: "1", Symbol: "AAPL", Name: "Apple Inc", Status: entities.AlpacaAssetStatusActive, Tradable: true},
			{ID: "2", Symbol: "GOOGL", Name: "Alphabet Inc", Status: entities.AlpacaAssetStatusActive, Tradable: true},
			{ID: "3", Symbol: "MSFT", Name: "Microsoft Corporation", Status: entities.AlpacaAssetStatusActive, Tradable: true},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	router := gin.New()
	router.GET("/api/v1/assets/search", handlers.SearchAssets)

	tests := []struct {
		name           string
		query          string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "missing search query",
			query:          "",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "INVALID_PARAMETER", errResp.Code)
			},
		},
		{
			name:           "valid search",
			query:          "?q=apple",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 1)
				assert.Equal(t, "AAPL", response.Assets[0].Symbol)
			},
		},
		{
			name:           "search with limit",
			query:          "?q=micro&limit=10",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 1)
				assert.Equal(t, "MSFT", response.Assets[0].Symbol)
			},
		},
		{
			name:           "case insensitive search",
			query:          "?q=ALPHABET",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 1)
				assert.Equal(t, "GOOGL", response.Assets[0].Symbol)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/api/v1/assets/search"+tt.query, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

func TestAlpacaHandlers_GetPopularAssets(t *testing.T) {
	handlers, server := setupAlpacaTest(t)
	defer server.Close()

	// Track which symbols were requested
	requestedSymbols := make(map[string]int)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract symbol from URL path
		path := r.URL.Path
		symbol := path[len("/v1/assets/"):]

		requestedSymbols[symbol]++

		// Return different responses based on symbol
		var response entities.AlpacaAssetResponse
		switch symbol {
		case "AAPL":
			response = entities.AlpacaAssetResponse{
				ID: "1", Symbol: "AAPL", Name: "Apple Inc",
				Status: entities.AlpacaAssetStatusActive, Tradable: true,
			}
		case "GOOGL":
			response = entities.AlpacaAssetResponse{
				ID: "2", Symbol: "GOOGL", Name: "Alphabet Inc",
				Status: entities.AlpacaAssetStatusActive, Tradable: true,
			}
		case "INVALID":
			// Simulate inactive asset
			response = entities.AlpacaAssetResponse{
				ID: "3", Symbol: "INVALID", Name: "Invalid Corp",
				Status: entities.AlpacaAssetStatusInactive, Tradable: false,
			}
		default:
			response = entities.AlpacaAssetResponse{
				ID: "4", Symbol: symbol, Name: symbol + " Corp",
				Status: entities.AlpacaAssetStatusActive, Tradable: true,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	router := gin.New()
	router.GET("/api/v1/assets/popular", handlers.GetPopularAssets)

	req, _ := http.NewRequest("GET", "/api/v1/assets/popular", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response AssetsResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Should return popular assets (filtered for active and tradable)
	assert.True(t, len(response.Assets) > 0, "Should return some popular assets")

	// Verify that popular symbols were requested
	assert.True(t, requestedSymbols["AAPL"] > 0, "Should request AAPL")
	assert.True(t, requestedSymbols["GOOGL"] > 0, "Should request GOOGL")

	// All returned assets should be active and tradable
	for _, asset := range response.Assets {
		assert.Equal(t, entities.AlpacaAssetStatusActive, asset.Status)
		assert.True(t, asset.Tradable)
	}
}

func TestAlpacaHandlers_GetAssetsByExchange(t *testing.T) {
	handlers, server := setupAlpacaTest(t)
	defer server.Close()

	// Override server to return exchange-filtered assets
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if exchange parameter is present
		exchange := r.URL.Query().Get("exchange")
		if exchange == "NASDAQ" {
			response := []entities.AlpacaAssetResponse{
				{ID: "1", Symbol: "AAPL", Name: "Apple Inc", Status: entities.AlpacaAssetStatusActive, Tradable: true},
				{ID: "2", Symbol: "MSFT", Name: "Microsoft Corp", Status: entities.AlpacaAssetStatusActive, Tradable: true},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		} else {
			// Return empty for other exchanges
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]entities.AlpacaAssetResponse{})
		}
	})

	router := gin.New()
	router.GET("/api/v1/assets/exchange/:exchange", handlers.GetAssetsByExchange)

	tests := []struct {
		name           string
		exchange       string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "valid exchange - NASDAQ",
			exchange:       "NASDAQ",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 2)
				assert.Equal(t, "AAPL", response.Assets[0].Symbol)
			},
		},
		{
			name:           "valid exchange with pagination",
			exchange:       "NASDAQ",
			queryParams:    "?page=1&page_size=1",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response AssetsResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Len(t, response.Assets, 1)
				assert.Equal(t, 2, response.TotalCount)
			},
		},
		{
			name:           "invalid exchange",
			exchange:       "INVALID",
			queryParams:    "",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var errResp ErrorResponse
				err := json.Unmarshal(body, &errResp)
				require.NoError(t, err)
				assert.Equal(t, "INVALID_EXCHANGE", errResp.Code)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v1/assets/exchange/" + tt.exchange + tt.queryParams
			req, _ := http.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

func TestErrorResponse(t *testing.T) {
	// Test ErrorResponse JSON marshaling
	errResp := ErrorResponse{
		Code:    "TEST_ERROR",
		Error:   "Test error message",
		Details: "Additional details",
	}

	data, err := json.Marshal(errResp)
	require.NoError(t, err)

	var unmarshaled ErrorResponse
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, errResp.Code, unmarshaled.Code)
	assert.Equal(t, errResp.Error, unmarshaled.Error)
	assert.Equal(t, errResp.Details, unmarshaled.Details)
}

func TestBuildMarketContext(t *testing.T) {
	context := buildMarketContext()

	// Should always return a valid market context
	assert.NotEmpty(t, context.Timezone)
	assert.Equal(t, "America/New_York", context.Timezone)

	// During test execution, we can't reliably predict market hours,
	// but the function should not panic and should return reasonable values
	assert.NotNil(t, context)
}

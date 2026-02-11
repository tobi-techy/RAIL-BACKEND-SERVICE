package investing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
	alpacaAdapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleMarketDataErrorMapsUnauthorizedToBadGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	h := &MarketHandlers{}

	err := fmt.Errorf("get quote: %w", &alpacaAdapter.ClientError{
		Message:    "auth failed",
		StatusCode: http.StatusUnauthorized,
	})
	h.handleMarketDataError(ctx, err, "fallback")

	require.Equal(t, http.StatusBadGateway, rec.Code)

	var response entities.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "UPSTREAM_AUTH_ERROR", response.Code)
	assert.Equal(t, "Market data provider authentication failed", response.Message)
}

func TestHandleMarketDataErrorMapsRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	h := &MarketHandlers{}

	err := fmt.Errorf("get quote: %w", &alpacaAdapter.RateLimitError{
		Message:            "rate limited",
		RetryAfterDuration: 0,
	})
	h.handleMarketDataError(ctx, err, "fallback")

	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	var response entities.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "SERVICE_UNAVAILABLE", response.Code)
	assert.Equal(t, "Market data provider rate limit exceeded", response.Message)
}

func TestHandleMarketDataErrorFallsBackToInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	h := &MarketHandlers{}

	h.handleMarketDataError(ctx, fmt.Errorf("boom"), "Failed to get quote")

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var response entities.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "INTERNAL_ERROR", response.Code)
	assert.Equal(t, "Failed to get quote", response.Message)
}

package investing

import (
	"errors"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/market"
	alpacaAdapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// MarketHandlers handles market data and alerts endpoints
type MarketHandlers struct {
	marketService *market.MarketDataService
	logger        *logger.Logger
}

func NewMarketHandlers(marketService *market.MarketDataService, logger *logger.Logger) *MarketHandlers {
	return &MarketHandlers{marketService: marketService, logger: logger}
}

// GetQuote returns real-time quote for a symbol
// GET /api/v1/market/quote/:symbol
func (h *MarketHandlers) GetQuote(c *gin.Context) {
	symbol := strings.ToUpper(c.Param("symbol"))
	if symbol == "" {
		common.RespondBadRequest(c, "Symbol required")
		return
	}

	quote, err := h.marketService.GetQuote(c.Request.Context(), symbol)
	if err != nil {
		h.logger.Error("Failed to get quote", "error", err, "symbol", symbol)
		h.handleMarketDataError(c, err, "Failed to get quote")
		return
	}

	c.JSON(http.StatusOK, quote)
}

// GetQuotes returns quotes for multiple symbols
// GET /api/v1/market/quotes?symbols=AAPL,GOOGL,MSFT
func (h *MarketHandlers) GetQuotes(c *gin.Context) {
	symbolsParam := c.Query("symbols")
	if symbolsParam == "" {
		common.RespondBadRequest(c, "Symbols required")
		return
	}

	symbols := strings.Split(strings.ToUpper(symbolsParam), ",")
	quotes, err := h.marketService.GetQuotes(c.Request.Context(), symbols)
	if err != nil {
		h.logger.Error("Failed to get quotes", "error", err)
		h.handleMarketDataError(c, err, "Failed to get quotes")
		return
	}

	c.JSON(http.StatusOK, gin.H{"quotes": quotes})
}

// GetBars returns historical OHLCV data
// GET /api/v1/market/bars/:symbol?timeframe=1Day&start=2024-01-01&end=2024-12-01
func (h *MarketHandlers) GetBars(c *gin.Context) {
	symbol := strings.ToUpper(c.Param("symbol"))
	timeframe := c.DefaultQuery("timeframe", "1Day")
	startStr := c.Query("start")
	endStr := c.Query("end")

	start, _ := time.Parse("2006-01-02", startStr)
	if start.IsZero() {
		start = time.Now().AddDate(0, -1, 0)
	}
	end, _ := time.Parse("2006-01-02", endStr)
	if end.IsZero() {
		end = time.Now()
	}

	bars, err := h.marketService.GetBars(c.Request.Context(), symbol, timeframe, start, end)
	if err != nil {
		h.logger.Error("Failed to get bars", "error", err, "symbol", symbol)
		h.handleMarketDataError(c, err, "Failed to get bars")
		return
	}

	c.JSON(http.StatusOK, gin.H{"bars": bars})
}

// CreateAlert creates a new market alert
// POST /api/v1/market/alerts
func (h *MarketHandlers) CreateAlert(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req struct {
		Symbol         string  `json:"symbol" binding:"required"`
		AlertType      string  `json:"alert_type" binding:"required"`
		ConditionValue float64 `json:"condition_value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request")
		return
	}

	alert, err := h.marketService.CreateAlert(c.Request.Context(), userID, strings.ToUpper(req.Symbol), req.AlertType, decimal.NewFromFloat(req.ConditionValue))
	if err != nil {
		h.logger.Error("Failed to create alert", "error", err)
		common.RespondBadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, alert)
}

// GetAlerts returns user's market alerts
// GET /api/v1/market/alerts
func (h *MarketHandlers) GetAlerts(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	alerts, err := h.marketService.GetUserAlerts(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get alerts", "error", err)
		common.RespondInternalError(c, "Failed to get alerts")
		return
	}

	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

// DeleteAlert deletes a market alert
// DELETE /api/v1/market/alerts/:id
func (h *MarketHandlers) DeleteAlert(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	alertID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		common.RespondBadRequest(c, "Invalid alert ID")
		return
	}

	if err := h.marketService.DeleteAlert(c.Request.Context(), userID, alertID); err != nil {
		if err.Error() == "forbidden" {
			h.logger.Warn("Unauthorized alert deletion attempt", "user_id", userID.String(), "alert_id", alertID.String())
			common.RespondError(c, http.StatusForbidden, "FORBIDDEN", "You do not own this alert", nil)
			return
		}
		h.logger.Error("Failed to delete alert", "error", err)
		common.RespondInternalError(c, "Failed to delete alert")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Alert deleted"})
}

func (h *MarketHandlers) handleMarketDataError(c *gin.Context, err error, fallbackMessage string) {
	var rateLimitErr *alpacaAdapter.RateLimitError
	var clientErr *alpacaAdapter.ClientError
	var serverErr *alpacaAdapter.ServerError

	switch {
	case errors.As(err, &rateLimitErr):
		common.RespondError(c, http.StatusTooManyRequests, common.ErrCodeServiceUnavailable, "Market data provider rate limit exceeded", nil)
	case errors.As(err, &clientErr):
		switch clientErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			common.RespondError(c, http.StatusBadGateway, "UPSTREAM_AUTH_ERROR", "Market data provider authentication failed", nil)
		case http.StatusNotFound:
			common.RespondNotFound(c, "Symbol not found")
		default:
			common.RespondError(c, http.StatusBadGateway, "UPSTREAM_REQUEST_FAILED", "Market data provider request failed", map[string]interface{}{
				"status_code": clientErr.StatusCode,
			})
		}
	case errors.As(err, &serverErr):
		common.RespondError(c, http.StatusBadGateway, common.ErrCodeServiceUnavailable, "Market data provider unavailable", nil)
	default:
		common.RespondInternalError(c, fallbackMessage)
	}
}

package investing

import (
	"errors"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
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

// GetExplore returns a UI-ready market explorer list.
// GET /api/v1/market/explore
func (h *MarketHandlers) GetExplore(c *gin.Context) {
	filters, err := parseMarketExploreFilters(c)
	if err != nil {
		common.RespondBadRequest(c, err.Error())
		return
	}

	resp, err := h.marketService.ExploreMarket(c.Request.Context(), filters)
	if err != nil {
		h.logger.Error("Failed to explore market", "error", err)
		h.handleMarketDataError(c, err, "Failed to explore market")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetInstrument returns details for a single market instrument.
// GET /api/v1/market/instruments/:symbol
func (h *MarketHandlers) GetInstrument(c *gin.Context) {
	symbol := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))
	if symbol == "" {
		common.RespondBadRequest(c, "Symbol required")
		return
	}

	includeBars := false
	for _, inc := range strings.Split(strings.ToLower(c.DefaultQuery("include", "")), ",") {
		if strings.TrimSpace(inc) == "bars" {
			includeBars = true
			break
		}
	}

	timeframe := c.DefaultQuery("bars_timeframe", "1Day")
	barsLimit, err := strconv.Atoi(c.DefaultQuery("bars_limit", "30"))
	if err != nil {
		common.RespondBadRequest(c, "Invalid bars_limit")
		return
	}
	if barsLimit < 1 {
		barsLimit = 30
	}
	if barsLimit > 365 {
		barsLimit = 365
	}

	resp, svcErr := h.marketService.GetMarketInstrument(c.Request.Context(), symbol, includeBars, timeframe, barsLimit)
	if svcErr != nil {
		h.logger.Error("Failed to get instrument", "error", svcErr, "symbol", symbol)
		h.handleMarketDataError(c, svcErr, "Failed to get instrument")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetFilterMetadata returns filter metadata for market explorer UIs.
// GET /api/v1/market/filters
func (h *MarketHandlers) GetFilterMetadata(c *gin.Context) {
	resp, err := h.marketService.GetMarketFilterMetadata(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get market filters", "error", err)
		h.handleMarketDataError(c, err, "Failed to get market filters")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetNews returns public market news stories.
// GET /api/v1/market/news
func (h *MarketHandlers) GetNews(c *gin.Context) {
	filters, err := parseMarketNewsFilters(c)
	if err != nil {
		common.RespondBadRequest(c, err.Error())
		return
	}

	resp, svcErr := h.marketService.GetMarketNews(c.Request.Context(), filters)
	if svcErr != nil {
		h.logger.Error("Failed to get market news", "error", svcErr)
		h.handleMarketDataError(c, svcErr, "Failed to get market news")
		return
	}

	c.JSON(http.StatusOK, resp)
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

func parseMarketExploreFilters(c *gin.Context) (entities.MarketExploreFilters, error) {
	var filters entities.MarketExploreFilters

	filters.Query = strings.TrimSpace(c.Query("q"))

	if types := strings.TrimSpace(c.Query("types")); types != "" {
		for _, raw := range strings.Split(types, ",") {
			t := entities.MarketInstrumentType(strings.ToLower(strings.TrimSpace(raw)))
			switch t {
			case entities.MarketInstrumentTypeStock, entities.MarketInstrumentTypeETF, entities.MarketInstrumentTypeBond, entities.MarketInstrumentTypeCrypto, entities.MarketInstrumentTypeOption:
				filters.Types = append(filters.Types, t)
			default:
				return filters, errors.New("Invalid type filter")
			}
		}
	}

	if exchanges := strings.TrimSpace(c.Query("exchanges")); exchanges != "" {
		for _, ex := range strings.Split(exchanges, ",") {
			ex = strings.TrimSpace(ex)
			if ex == "" {
				continue
			}
			filters.Exchanges = append(filters.Exchanges, ex)
		}
	}

	if categories := strings.TrimSpace(c.Query("categories")); categories != "" {
		for _, category := range strings.Split(categories, ",") {
			category = strings.TrimSpace(category)
			if category == "" {
				continue
			}
			filters.Categories = append(filters.Categories, category)
		}
	}

	if val, ok, err := parseOptionalBool(c.Query("tradable")); err != nil {
		return filters, err
	} else if ok {
		filters.Tradable = &val
	}
	if val, ok, err := parseOptionalBool(c.Query("fractionable")); err != nil {
		return filters, err
	} else if ok {
		filters.Fractionable = &val
	}
	if val, ok, err := parseOptionalBool(c.Query("marginable")); err != nil {
		return filters, err
	} else if ok {
		filters.Marginable = &val
	}
	if val, ok, err := parseOptionalBool(c.Query("shortable")); err != nil {
		return filters, err
	} else if ok {
		filters.Shortable = &val
	}

	if val, ok, err := parseOptionalDecimal(c.Query("min_price")); err != nil {
		return filters, err
	} else if ok {
		filters.MinPrice = &val
	}
	if val, ok, err := parseOptionalDecimal(c.Query("max_price")); err != nil {
		return filters, err
	} else if ok {
		filters.MaxPrice = &val
	}
	if val, ok, err := parseOptionalDecimal(c.Query("min_change_pct")); err != nil {
		return filters, err
	} else if ok {
		filters.MinChangePct = &val
	}
	if val, ok, err := parseOptionalDecimal(c.Query("max_change_pct")); err != nil {
		return filters, err
	} else if ok {
		filters.MaxChangePct = &val
	}

	filters.SortBy = strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort_by", "symbol")))
	filters.SortOrder = strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort_order", "asc")))

	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		return filters, errors.New("Invalid page parameter")
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if err != nil {
		return filters, errors.New("Invalid page_size parameter")
	}
	filters.Page = page
	filters.PageSize = pageSize

	return filters, nil
}

func parseOptionalBool(raw string) (bool, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, false, nil
	}
	if strings.EqualFold(trimmed, "true") || trimmed == "1" {
		return true, true, nil
	}
	if strings.EqualFold(trimmed, "false") || trimmed == "0" {
		return false, true, nil
	}
	return false, false, errors.New("Invalid boolean filter")
}

func parseOptionalDecimal(raw string) (decimal.Decimal, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return decimal.Zero, false, nil
	}
	value, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, false, errors.New("Invalid numeric filter")
	}
	return value, true, nil
}

func parseMarketNewsFilters(c *gin.Context) (entities.MarketNewsFilters, error) {
	filters := entities.MarketNewsFilters{}

	limit, err := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("limit", "10")))
	if err != nil {
		return filters, errors.New("Invalid limit parameter")
	}
	filters.Limit = limit

	if symbols := strings.TrimSpace(c.Query("symbols")); symbols != "" {
		for _, raw := range strings.Split(symbols, ",") {
			symbol := strings.ToUpper(strings.TrimSpace(raw))
			if symbol == "" {
				continue
			}
			filters.Symbols = append(filters.Symbols, symbol)
		}
	}

	filters.PageToken = strings.TrimSpace(c.Query("page_token"))
	if value, ok, err := parseOptionalBool(c.Query("include_content")); err != nil {
		return filters, err
	} else if ok {
		filters.IncludeContent = value
	}

	return filters, nil
}

// GetMarketStatus returns whether the US stock market is currently open.
// GET /api/v1/market/status
// No auth required — safe to call unauthenticated.
func (h *MarketHandlers) GetMarketStatus(c *gin.Context) {
	now := time.Now().UTC()

	// NYSE hours: Mon–Fri 09:30–16:00 ET (UTC-5 standard, UTC-4 daylight)
	// Use America/New_York for correct DST handling.
	loc, _ := time.LoadLocation("America/New_York")
	et := now.In(loc)

	weekday := et.Weekday()
	isWeekday := weekday >= time.Monday && weekday <= time.Friday

	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)

	isOpen := isWeekday && et.After(open) && et.Before(close)

	// Calculate next open time
	nextOpen := nextMarketOpen(et, loc)

	c.JSON(http.StatusOK, gin.H{
		"is_open":      isOpen,
		"next_open":    nextOpen.UTC().Format(time.RFC3339),
		"next_open_et": nextOpen.In(loc).Format("Mon Jan 2, 3:04 PM MST"),
		"current_time": now.Format(time.RFC3339),
		"timezone":     "America/New_York",
	})
}

func nextMarketOpen(et time.Time, loc *time.Location) time.Time {
	// Start from tomorrow if market is closed for today or already past close
	candidate := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	open := candidate
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)

	isWeekday := et.Weekday() >= time.Monday && et.Weekday() <= time.Friday
	if isWeekday && et.Before(open) {
		return open // market opens later today
	}
	if isWeekday && et.After(open) && et.Before(close) {
		return open // market is open now — return today's open (caller checks is_open)
	}

	// Advance to next weekday
	next := et.AddDate(0, 0, 1)
	for {
		if next.Weekday() >= time.Monday && next.Weekday() <= time.Friday {
			break
		}
		next = next.AddDate(0, 0, 1)
	}
	return time.Date(next.Year(), next.Month(), next.Day(), 9, 30, 0, 0, loc)
}

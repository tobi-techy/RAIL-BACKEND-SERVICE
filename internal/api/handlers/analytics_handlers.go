package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AnalyticsHandlers handles portfolio analytics endpoints
type AnalyticsHandlers struct {
	analyticsService *analytics.PortfolioAnalyticsService
	logger           *logger.Logger
}

func NewAnalyticsHandlers(analyticsService *analytics.PortfolioAnalyticsService, logger *logger.Logger) *AnalyticsHandlers {
	return &AnalyticsHandlers{analyticsService: analyticsService, logger: logger}
}

// GetPerformanceMetrics returns portfolio performance metrics
// GET /api/v1/analytics/performance
func (h *AnalyticsHandlers) GetPerformanceMetrics(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	metrics, err := h.analyticsService.GetPerformanceMetrics(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get performance metrics", "error", err)
		respondInternalError(c, "Failed to get performance metrics")
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// GetRiskMetrics returns portfolio risk assessment
// GET /api/v1/analytics/risk
func (h *AnalyticsHandlers) GetRiskMetrics(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	metrics, err := h.analyticsService.GetRiskMetrics(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get risk metrics", "error", err)
		respondInternalError(c, "Failed to get risk metrics")
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// GetDiversificationAnalysis returns portfolio diversification analysis
// GET /api/v1/analytics/diversification
func (h *AnalyticsHandlers) GetDiversificationAnalysis(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	analysis, err := h.analyticsService.GetDiversificationAnalysis(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get diversification analysis", "error", err)
		respondInternalError(c, "Failed to get diversification analysis")
		return
	}

	c.JSON(http.StatusOK, analysis)
}

// TakeSnapshot captures current portfolio state
// POST /api/v1/analytics/snapshot
func (h *AnalyticsHandlers) TakeSnapshot(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	if err := h.analyticsService.TakeSnapshot(c.Request.Context(), userID); err != nil {
		h.logger.Error("Failed to take snapshot", "error", err)
		respondInternalError(c, "Failed to take snapshot")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Snapshot captured"})
}

// GetPortfolioHistory returns historical portfolio data for charting
// GET /api/v1/analytics/history?period=1M
func (h *AnalyticsHandlers) GetPortfolioHistory(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	period := c.DefaultQuery("period", "1M")
	validPeriods := map[string]bool{"1D": true, "1W": true, "1M": true, "3M": true, "6M": true, "1Y": true, "ALL": true}
	if !validPeriods[period] {
		respondBadRequest(c, "Invalid period. Valid values: 1D, 1W, 1M, 3M, 6M, 1Y, ALL")
		return
	}

	history, err := h.analyticsService.GetPortfolioHistory(c.Request.Context(), userID, period)
	if err != nil {
		h.logger.Error("Failed to get portfolio history", "error", err, "period", period)
		respondInternalError(c, "Failed to get portfolio history")
		return
	}

	c.JSON(http.StatusOK, history)
}

// GetDashboard returns comprehensive portfolio dashboard
// GET /api/v1/analytics/dashboard
func (h *AnalyticsHandlers) GetDashboard(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		respondUnauthorized(c, "User not authenticated")
		return
	}

	dashboard, err := h.analyticsService.GetDashboard(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get portfolio dashboard", "error", err)
		respondInternalError(c, "Failed to get portfolio dashboard")
		return
	}

	c.JSON(http.StatusOK, dashboard)
}

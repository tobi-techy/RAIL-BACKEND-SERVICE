package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/yield"
	recon "github.com/rail-service/rail_service/internal/workers/reconciliation"
	yield_distribution "github.com/rail-service/rail_service/internal/workers/yield_distribution"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// internalAPY is used only for the daily estimate calculation — never exposed in the API response.
const internalAPY = "0.045"

type YieldHandlers struct {
	yieldSvc      *yield.Service
	allocationSvc *allocation.Service
	logger        *zap.Logger
}

func NewYieldHandlers(yieldSvc *yield.Service, allocationSvc *allocation.Service, logger *zap.Logger) *YieldHandlers {
	return &YieldHandlers{yieldSvc: yieldSvc, allocationSvc: allocationSvc, logger: logger}
}

// GetDailyYieldEstimate returns the estimated daily yield for the authenticated user.
// GET /v1/yield/estimate
func (h *YieldHandlers) GetDailyYieldEstimate(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	balances, err := h.allocationSvc.GetBalances(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get user balances for yield estimate", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get balances"})
		return
	}

	apy, err := decimal.NewFromString(internalAPY)
	if err != nil {
		h.logger.Error("Failed to parse internal APY constant", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to calculate yield estimate"})
		return
	}
	daily := h.yieldSvc.EstimateDailyYield(balances.StashBalance, apy)

	c.JSON(http.StatusOK, gin.H{
		"stash_balance":  balances.StashBalance.StringFixed(6),
		"daily_estimate": daily.StringFixed(6),
	})
}

// TriggerStashReconciliation runs a manual stash reconciliation check.
// POST /admin/stash/reconcile
func TriggerStashReconciliation(worker *recon.Worker, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := worker.Run(c.Request.Context()); err != nil {
			logger.Error("Stash reconciliation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}
// TriggerYieldDistribution triggers yield distribution asynchronously.
// Returns 202 immediately; check logs for completion.
// POST /admin/yield/distribute
// Body: { "period_start": "2026-03-01", "period_end": "2026-03-31" }
func TriggerYieldDistribution(worker *yield_distribution.Worker, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			PeriodStart string `json:"period_start" binding:"required"`
			PeriodEnd   string `json:"period_end" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		start, err := time.Parse(time.DateOnly, req.PeriodStart)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "period_start must be YYYY-MM-DD"})
			return
		}
		end, err := time.Parse(time.DateOnly, req.PeriodEnd)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "period_end must be YYYY-MM-DD"})
			return
		}
		if !end.After(start) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "period_end must be after period_start"})
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := worker.Run(ctx, start, end); err != nil {
				logger.Error("Async yield distribution failed", zap.Error(err),
					zap.String("period_start", req.PeriodStart),
					zap.String("period_end", req.PeriodEnd),
				)
			}
		}()
		c.JSON(http.StatusAccepted, gin.H{"status": "accepted", "period_start": req.PeriodStart, "period_end": req.PeriodEnd})
	}
}
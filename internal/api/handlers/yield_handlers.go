package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/yield"
	recon "github.com/rail-service/rail_service/internal/workers/reconciliation"
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An unexpected error occurred. Please try again."})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

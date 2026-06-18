package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"go.uber.org/zap"
)

// BlendHandlers exposes read-only Blend yield endpoints for the frontend.
type BlendHandlers struct {
	router *blend.DepositRouter
	logger *zap.Logger
}

func NewBlendHandlers(router *blend.DepositRouter, logger *zap.Logger) *BlendHandlers {
	return &BlendHandlers{router: router, logger: logger}
}

// GetYieldOverview returns the authenticated user's Blend yield snapshot.
// GET /v1/yield/blend/overview
func (h *BlendHandlers) GetYieldOverview(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if h.router == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "blend yield is not enabled"})
		return
	}
	overview, err := h.router.GetUserYieldOverview(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get Blend yield overview", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load yield overview"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active":         overview.Active,
		"chain_id":       overview.ChainID,
		"safe_address":   overview.SafeAddress,
		"principal":      overview.Principal.StringFixed(6),
		"current_value":  overview.CurrentValue.StringFixed(6),
		"yield_earned":   overview.YieldEarned.StringFixed(6),
		"returns_pct":    overview.ReturnsPct,
		"pending_amount": overview.PendingAmount.StringFixed(6),
	})
}

package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	usagesvc "github.com/rail-service/rail_service/internal/domain/services/usage"
	"go.uber.org/zap"
)

// UsageHandlers handles AI usage endpoints.
type UsageHandlers struct {
	service *usagesvc.Service
	logger  *zap.Logger
}

// NewUsageHandlers creates new usage handlers.
func NewUsageHandlers(service *usagesvc.Service, logger *zap.Logger) *UsageHandlers {
	return &UsageHandlers{service: service, logger: logger}
}

// GetUsage handles GET /api/v1/ai/usage
func (h *UsageHandlers) GetUsage(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	usage, err := h.service.GetCurrentUsage(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get usage failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get usage"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"message_count":     usage.MessageCount,
			"voice_seconds":     usage.VoiceSeconds,
			"estimated_cost":    usage.EstimatedCost,
			"model_calls":       usage.ModelCalls,
			"period_start":      usage.PeriodStart,
			"over_cost_ceiling": usage.EstimatedCost.GreaterThanOrEqual(entities.CostCeilingUSD),
		},
	})
}

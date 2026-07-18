package investing

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
)

// EnhancedNudgeHandler handles context-aware nudge requests.
type EnhancedNudgeHandler struct {
	orchestrator aiservice.ChatEngine
	logger       *zap.Logger
}

func NewEnhancedNudgeHandler(orchestrator aiservice.ChatEngine, logger *zap.Logger) *EnhancedNudgeHandler {
	return &EnhancedNudgeHandler{orchestrator: orchestrator, logger: logger}
}

// HandleEnhancedNudge handles POST /v1/ai/nudge/enhanced
func (h *EnhancedNudgeHandler) HandleEnhancedNudge(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}

	var req entities.EnhancedNudgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Screen) == "" {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "screen is required")
		return
	}

	resp, err := h.orchestrator.GenerateEnhancedNudge(c.Request.Context(), userID, req)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("enhanced nudge failed", zap.Error(err))
		}
		c.JSON(http.StatusOK, entities.EnhancedNudgeResponse{Show: false, Severity: "info"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

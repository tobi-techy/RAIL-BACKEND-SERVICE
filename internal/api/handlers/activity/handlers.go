package activity

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	activitysvc "github.com/rail-service/rail_service/internal/domain/services/activity"
	"go.uber.org/zap"
)

type Handlers struct {
	service *activitysvc.Service
	logger  *zap.Logger
}

func NewHandlers(service *activitysvc.Service, logger *zap.Logger) *Handlers {
	return &Handlers{service: service, logger: logger}
}

// GetActivityFeed handles GET /api/v1/activity
func (h *Handlers) GetActivityFeed(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	feed, err := h.service.GetActivityFeed(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get activity feed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load activity"})
		return
	}

	c.JSON(http.StatusOK, feed)
}

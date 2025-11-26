package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/infrastructure/adapters"
)

func AuditMiddleware(auditService *adapters.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			return
		}

		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			return
		}

		action := c.Request.Method + "_" + c.FullPath()
		resource := c.FullPath()

		// Log using infrastructure audit service
		auditService.LogSystemEvent(c.Request.Context(), action, resource, map[string]interface{}{
			"user_id":    userID.String(),
			"ip_address": c.ClientIP(),
			"user_agent": c.Request.UserAgent(),
		})
	}
}

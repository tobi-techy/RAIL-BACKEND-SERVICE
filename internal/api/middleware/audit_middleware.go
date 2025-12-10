package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
)

// AuditContext middleware adds IP address and user agent to context for audit logging
func AuditContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		ipAddress := c.ClientIP()
		userAgent := c.Request.UserAgent()

		var userID *uuid.UUID
		if id, exists := c.Get("user_id"); exists {
			if uid, ok := id.(uuid.UUID); ok {
				userID = &uid
			}
		}

		ctx := audit.WithAuditContext(c.Request.Context(), ipAddress, userAgent, userID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

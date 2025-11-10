package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
)

func AuditMiddleware(auditService *services.AuditService) gin.HandlerFunc {
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

		action := entities.AuditAction(c.Request.Method + "_" + c.FullPath())

		auditService.Log(c.Request.Context(), &entities.AuditLog{
			UserID:    userID,
			Action:    action,
			Resource:  c.FullPath(),
			IPAddress: c.ClientIP(),
			UserAgent: c.Request.UserAgent(),
		})
	}
}

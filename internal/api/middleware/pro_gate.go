package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProChecker checks if a user has an active Pro subscription
type ProChecker interface {
	IsProUser(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ProGate returns middleware that blocks non-Pro users with 403
func ProGate(checker ProChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if checker == nil {
			c.Next()
			return
		}
		userIDStr, exists := c.Get("user_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		userID, ok := userIDStr.(uuid.UUID)
		if !ok {
			str, strOk := userIDStr.(string)
			if !strOk {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid user"})
				return
			}
			parsed, err := uuid.Parse(str)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid user"})
				return
			}
			userID = parsed
		}
		isPro, err := checker.IsProUser(c.Request.Context(), userID)
		if err != nil || !isPro {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "pro_required",
				"message": "This feature requires a Rail Pro subscription",
			})
			return
		}
		c.Next()
	}
}

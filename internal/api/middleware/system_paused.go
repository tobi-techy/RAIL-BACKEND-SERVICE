package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// SystemPaused returns a middleware that blocks all requests with 503
// when the SYSTEM_PAUSED environment variable is set to "true".
// Apply to deposit, allocation, and yield routes.
func SystemPaused() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("SYSTEM_PAUSED")), "true") {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "system_paused",
				"message": "Rail is temporarily paused for maintenance. Please try again shortly.",
			})
			return
		}
		c.Next()
	}
}

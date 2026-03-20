package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// SystemPaused returns a middleware that blocks all requests with 503
// when the SYSTEM_PAUSED environment variable is set to "true" at startup.
// NOTE: The value is read once at middleware creation time. Runtime changes
// require an application restart.
func SystemPaused() gin.HandlerFunc {
	paused := strings.EqualFold(strings.TrimSpace(os.Getenv("SYSTEM_PAUSED")), "true")
	return func(c *gin.Context) {
		if paused {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":   "system_paused",
				"message": "Rail is temporarily paused for maintenance. Please try again shortly.",
			})
			return
		}
		c.Next()
	}
}

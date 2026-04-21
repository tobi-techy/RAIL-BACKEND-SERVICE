package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/pkg/alerting"
)

// ErrorAlerter sends Telegram alerts for 5xx responses.
func ErrorAlerter(t *alerting.TelegramAlerter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if t == nil {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		if status < 500 {
			return
		}

		go t.SendAlert(alerting.ErrorDetails{
			RequestID:  c.GetString("request_id"),
			Method:     c.Request.Method,
			Path:       c.Request.URL.String(),
			StatusCode: status,
			ClientIP:   c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			Latency:    time.Since(start),
		})
	}
}

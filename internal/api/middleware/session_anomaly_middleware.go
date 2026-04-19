package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/services/security"
)

// SessionAnomalyDetection runs session anomaly detection after successful authentication.
// Attach to authenticated routes — it checks for impossible travel, concurrent country sessions, etc.
// Anomalies are logged but do not block the request (detection-only mode).
func SessionAnomalyDetection(svc *security.SessionAnomalyService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next() // run handler first

		// Only analyze if the request succeeded and user is authenticated
		if c.Writer.Status() >= 400 {
			return
		}
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			return
		}
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			return
		}

		sc := security.SessionContext{
			UserID:    userID,
			IP:        c.ClientIP(),
			UserAgent: c.GetHeader("User-Agent"),
			Country:   c.GetHeader("CF-IPCountry"), // Cloudflare geo header
			City:      "",
		}

		anomalies := svc.AnalyzeSession(c.Request.Context(), sc)
		if len(anomalies) > 0 {
			logger.Warn("Session anomalies detected",
				zap.String("user_id", userIDStr),
				zap.Int("count", len(anomalies)),
				zap.String("ip", sc.IP))
		}
	}
}

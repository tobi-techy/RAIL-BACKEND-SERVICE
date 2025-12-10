package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/services"
	"go.uber.org/zap"
)

func FraudDetectionMiddleware(controlService *services.TransactionControlService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			c.Next()
			return
		}

		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.Next()
			return
		}

		var body map[string]interface{}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.Next()
			return
		}

		if amountVal, ok := body["amount"]; ok {
			amount := decimal.NewFromFloat(amountVal.(float64))
			score, factors := controlService.CalculateFraudScore(c.Request.Context(), userID, amount, c.FullPath())

			if score.GreaterThan(decimal.NewFromFloat(0.7)) {
				logger.Warn("High fraud risk detected",
					zap.String("user_id", userID.String()),
					zap.String("score", score.String()),
					zap.Any("factors", factors))

				c.JSON(http.StatusForbidden, gin.H{"error": "Transaction flagged for review"})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

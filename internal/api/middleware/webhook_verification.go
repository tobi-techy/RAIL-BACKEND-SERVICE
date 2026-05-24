package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/pkg/webhook"
)

// WebhookProviderConfig holds per-provider webhook secrets
type WebhookProviderConfig struct {
	BridgeSecret string
	AlpacaSecret string
}

// HardenedWebhookVerification creates middleware for per-provider webhook signature + timestamp validation
func HardenedWebhookVerification(config WebhookProviderConfig, logger *zap.Logger) gin.HandlerFunc {
	validators := map[string]*webhook.WebhookValidator{}

	if config.BridgeSecret != "" {
		validators["bridge"] = webhook.NewWebhookValidator(webhook.WebhookSecurityConfig{
			Secret: config.BridgeSecret, MaxTimestampAge: 300, RequireSignature: true,
		})
	}
	if config.AlpacaSecret != "" {
		validators["alpaca"] = webhook.NewWebhookValidator(webhook.WebhookSecurityConfig{
			Secret: config.AlpacaSecret, MaxTimestampAge: 300, RequireSignature: true,
		})
	}

	return func(c *gin.Context) {
		provider := resolveWebhookProvider(c)
		validator, exists := validators[provider]
		if !exists {
			logger.Warn("No webhook validator for provider", zap.String("provider", provider))
			c.Next()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "INVALID_BODY"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

		signature := extractSignature(c)
		timestamp := time.Now().Unix()

		if ts := c.GetHeader("X-Webhook-Timestamp"); ts != "" {
			if parsed, err := strconv.ParseInt(ts, 10, 64); err == nil {
				timestamp = parsed
			}
		}

		if err := validator.ValidateRequest(body, signature, timestamp, c.GetHeader("User-Agent")); err != nil {
			logger.Warn("Webhook verification failed",
				zap.String("provider", provider),
				zap.String("ip", c.ClientIP()),
				zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "WEBHOOK_VERIFICATION_FAILED"})
			return
		}

		c.Next()
	}
}

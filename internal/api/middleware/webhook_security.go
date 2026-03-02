package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	redisv8 "github.com/go-redis/redis/v8"
	"github.com/rail-service/rail_service/pkg/security"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// WebhookSecurityConfig holds webhook security configuration
type WebhookSecurityConfig struct {
	// Provider-specific webhook secrets for signature verification
	Secrets map[string]string
	// Provider-specific IP whitelists (CIDR notation)
	IPWhitelists map[string][]string
	// Provider-specific rate limits
	RateLimits map[string]security.WebhookRateLimit
	// Skip verification in development
	SkipVerification bool
}

// DefaultWebhookSecurityConfig returns sensible defaults
func DefaultWebhookSecurityConfig() WebhookSecurityConfig {
	return WebhookSecurityConfig{
		Secrets:      make(map[string]string),
		IPWhitelists: defaultWebhookIPWhitelists(),
		RateLimits:   defaultWebhookRateLimits(),
	}
}

// defaultWebhookIPWhitelists returns known webhook source IPs
func defaultWebhookIPWhitelists() map[string][]string {
	return map[string][]string{
		// Circle does not publish a fixed IP range — rely on signature verification instead.
		// Bridge webhook IPs (example - verify with Bridge docs)
		"bridge": {
			"34.102.136.180/32", // Bridge production
			"35.186.224.25/32",  // Bridge production
		},
		// Alpaca does not publish a fixed IP range — rely on signature verification instead.
		// Remove this entry and ensure ALPACA_WEBHOOK_SECRET is set for HMAC validation.
		"alpaca": {},
	}
}

// defaultWebhookRateLimits returns default rate limits per provider
func defaultWebhookRateLimits() map[string]security.WebhookRateLimit {
	return map[string]security.WebhookRateLimit{
		"circle":  {MaxRequests: 1000, Window: time.Minute},
		"bridge":  {MaxRequests: 500, Window: time.Minute},
		"alpaca":  {MaxRequests: 2000, Window: time.Minute},
		"default": {MaxRequests: 100, Window: time.Minute},
	}
}

// WebhookSecurity creates middleware for webhook endpoint protection
func WebhookSecurity(
	redisClient *redis.Client,
	config WebhookSecurityConfig,
	logger *zap.Logger,
) gin.HandlerFunc {
	var ipWhitelist *security.WebhookIPWhitelist
	var rateLimiter *security.WebhookRateLimiter
	var replayProtection *security.WebhookReplayProtection

	if !config.SkipVerification {
		ipWhitelist = security.NewWebhookIPWhitelist(config.IPWhitelists, logger)
		rateLimiter = security.NewWebhookRateLimiter(redisClient, config.RateLimits, logger)
		replayProtection = security.NewWebhookReplayProtection(
			redisClient,
			config.Secrets,
			security.DefaultWebhookReplayConfig(),
			logger,
		)
	}

	return func(c *gin.Context) {
		if config.SkipVerification {
			c.Next()
			return
		}

		// Extract provider from path/headers (funding webhooks multiplex providers).
		provider := resolveWebhookProvider(c)
		if provider == "" {
			provider = "unknown"
		}
		if provider == "unknown" && isUnifiedFundingWebhook(c.Request.URL.Path) {
			provider = detectProviderFromBody(c)
		}
		if provider == "unknown" || provider == "" {
			if isUnifiedFundingWebhook(c.Request.URL.Path) {
				logger.Warn("Unable to resolve provider for unified funding webhook")
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"error":   "UNKNOWN_WEBHOOK_PROVIDER",
					"message": "Unable to determine webhook provider",
				})
				return
			}
		}

		// 1. IP Whitelist check
		if ipWhitelist != nil {
			clientIP := c.ClientIP()
			if err := ipWhitelist.ValidateIP(provider, clientIP); err != nil {
				logger.Warn("Webhook IP rejected",
					zap.String("provider", provider),
					zap.String("client_ip", clientIP),
					zap.Error(err))
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error":   "IP_NOT_WHITELISTED",
					"message": "Request origin not authorized",
				})
				return
			}
		}

		// 2. Rate limit check
		if rateLimiter != nil {
			ctx := c.Request.Context()
			allowed, resetIn, err := rateLimiter.CheckRateLimit(ctx, provider)
			if err != nil {
				logger.Error("Rate limit check failed", zap.Error(err))
				// Fail open - don't block on Redis errors
			} else if !allowed {
				logger.Warn("Webhook rate limited",
					zap.String("provider", provider),
					zap.Duration("reset_in", resetIn))
				c.Header("Retry-After", resetIn.String())
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error":               "RATE_LIMITED",
					"message":             "Too many webhook requests",
					"retry_after_seconds": int(resetIn.Seconds()),
				})
				return
			}
		}

		// 3. Read body for replay protection (preserve for handler)
		if replayProtection != nil && len(config.Secrets) > 0 {
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"error":   "INVALID_BODY",
					"message": "Failed to read request body",
				})
				return
			}
			// Restore body for downstream handlers
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

			// Extract metadata and validate
			eventID, nonce, timestamp := security.ExtractWebhookMetadata(body)
			signature := extractSignature(c)

			if signature != "" {
				ctx := c.Request.Context()
				_, err := replayProtection.ValidateWebhook(ctx, body, signature, provider, eventID, nonce, timestamp)
				if err != nil {
					logger.Warn("Webhook validation failed",
						zap.String("provider", provider),
						zap.Error(err))
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
						"error":   "WEBHOOK_VALIDATION_FAILED",
						"message": "Webhook signature or replay validation failed",
					})
					return
				}
			}
		}

		c.Next()
	}
}

// extractProviderFromPath extracts the provider name from webhook path
func extractProviderFromPath(path string) string {
	// Expected paths: /api/v1/webhooks/circle, /webhooks/bridge, etc.
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if part == "webhooks" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func resolveWebhookProvider(c *gin.Context) string {
	provider := strings.ToLower(strings.TrimSpace(extractProviderFromPath(c.Request.URL.Path)))
	if provider != "" && provider != "funding" {
		return provider
	}

	if source := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Webhook-Source"))); source != "" {
		switch source {
		case "bridge", "circle", "alpaca":
			return source
		}
	}

	if c.GetHeader("X-Circle-Signature") != "" {
		return "circle"
	}
	if c.GetHeader("X-Bridge-Signature") != "" || c.GetHeader("Bridge-Signature") != "" {
		return "bridge"
	}
	if c.GetHeader("X-Alpaca-Signature") != "" || c.GetHeader("Alpaca-Signature") != "" {
		return "alpaca"
	}

	return "unknown"
}

func isUnifiedFundingWebhook(path string) bool {
	return strings.HasSuffix(strings.TrimSpace(path), "/webhooks/funding")
}

// detectProviderFromBody peeks at the request body to identify the webhook provider.
// It restores the body so downstream handlers can still read it.
func detectProviderFromBody(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	var peek map[string]interface{}
	if json.Unmarshal(body, &peek) != nil {
		return ""
	}
	if _, ok := peek["notificationType"]; ok {
		return "circle"
	}
	if _, ok := peek["event_category"]; ok {
		return "bridge"
	}
	if _, ok := peek["event"]; ok {
		return "alpaca"
	}
	return ""
}

// extractSignature extracts webhook signature from common header locations
func extractSignature(c *gin.Context) string {
	// Try common signature headers
	headers := []string{
		"X-Signature",
		"X-Hub-Signature-256",
		"X-Webhook-Signature",
		"Stripe-Signature",
		"X-Circle-Signature",
		"X-Bridge-Signature",
		"X-Alpaca-Signature",
	}
	for _, h := range headers {
		if sig := c.GetHeader(h); sig != "" {
			return sig
		}
	}
	return ""
}

func buildWebhookReplayKey(provider, path, signature, eventID, nonce string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(provider))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write([]byte(signature))
	h.Write([]byte{0})
	h.Write([]byte(eventID))
	h.Write([]byte{0})
	h.Write([]byte(nonce))
	h.Write([]byte{0})
	h.Write(body)
	return "webhook:replay:" + hex.EncodeToString(h.Sum(nil))
}

// WebhookSecurityWithRedisV8 creates middleware using Redis v8 client
// This is a compatibility wrapper for codebases using go-redis/redis/v8
func WebhookSecurityWithRedisV8(
	redisClient *redisv8.Client,
	config WebhookSecurityConfig,
	logger *zap.Logger,
) gin.HandlerFunc {
	// For v8 compatibility, we provide IP whitelisting, basic rate limiting,
	// and lightweight replay dedupe based on signed payload fingerprints.

	var ipWhitelist *security.WebhookIPWhitelist

	if !config.SkipVerification {
		ipWhitelist = security.NewWebhookIPWhitelist(config.IPWhitelists, logger)
	}

	return func(c *gin.Context) {
		if config.SkipVerification {
			c.Next()
			return
		}

		provider := resolveWebhookProvider(c)
		if provider == "" {
			provider = "unknown"
		}
		if provider == "unknown" && isUnifiedFundingWebhook(c.Request.URL.Path) {
			provider = detectProviderFromBody(c)
		}
		if provider == "unknown" || provider == "" {
			if isUnifiedFundingWebhook(c.Request.URL.Path) {
				logger.Warn("Unable to resolve provider for unified funding webhook")
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"error":   "UNKNOWN_WEBHOOK_PROVIDER",
					"message": "Unable to determine webhook provider",
				})
				return
			}
		}

		// IP Whitelist check
		if ipWhitelist != nil {
			clientIP := c.ClientIP()
			if err := ipWhitelist.ValidateIP(provider, clientIP); err != nil {
				logger.Warn("Webhook IP rejected",
					zap.String("provider", provider),
					zap.String("client_ip", clientIP),
					zap.Error(err))
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error":   "IP_NOT_WHITELISTED",
					"message": "Request origin not authorized",
				})
				return
			}
		}

		// Simple rate limiting using Redis v8
		if redisClient != nil {
			ctx := c.Request.Context()
			key := "webhook:rate:" + provider + ":" + time.Now().Format("2006010215")

			count, err := redisClient.Incr(ctx, key).Result()
			if err == nil {
				if count == 1 {
					redisClient.Expire(ctx, key, time.Hour)
				}
				// Default limit: 1000 requests per hour per provider
				limit := int64(1000)
				if l, ok := config.RateLimits[provider]; ok {
					limit = int64(l.MaxRequests)
				}
				if count > limit {
					logger.Warn("Webhook rate limited",
						zap.String("provider", provider),
						zap.Int64("count", count))
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
						"error":   "RATE_LIMITED",
						"message": "Too many webhook requests",
					})
					return
				}
			}
		}

		// Lightweight replay protection for v8 path: dedupe signed payloads in Redis.
		if redisClient != nil {
			ctx := c.Request.Context()
			signature := extractSignature(c)
			if signature != "" {
				body, err := io.ReadAll(c.Request.Body)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
						"error":   "INVALID_BODY",
						"message": "Failed to read request body",
					})
					return
				}
				// Restore body for downstream handlers.
				c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

				eventID, nonce, _ := security.ExtractWebhookMetadata(body)
				replayKey := buildWebhookReplayKey(provider, c.Request.URL.Path, signature, eventID, nonce, body)
				ok, err := redisClient.SetNX(ctx, replayKey, "1", 10*time.Minute).Result()
				if err != nil {
					logger.Error("Webhook replay check failed (v8 path); failing open", zap.Error(err))
				} else if !ok {
					logger.Warn("Webhook replay detected",
						zap.String("provider", provider),
						zap.String("path", c.Request.URL.Path))
					// Return 200 for replays — the original was already processed.
					// Returning non-2XX causes providers like Circle to retry indefinitely.
					c.AbortWithStatusJSON(http.StatusOK, gin.H{
						"status":  "already_processed",
						"message": "Duplicate webhook request",
					})
					return
				}
			}
		}

		c.Next()
	}
}

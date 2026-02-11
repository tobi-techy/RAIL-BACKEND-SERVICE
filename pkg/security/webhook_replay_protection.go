package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// WebhookReplayProtection provides replay attack prevention for webhooks
type WebhookReplayProtection struct {
	redis       *redis.Client
	logger      *zap.Logger
	secrets     map[string]string // provider -> secret
	windowSize  time.Duration
	maxNonceAge time.Duration
}

// WebhookReplayConfig holds configuration for replay protection
type WebhookReplayConfig struct {
	WindowSize  time.Duration
	MaxNonceAge time.Duration
}

// DefaultWebhookReplayConfig returns sensible defaults
func DefaultWebhookReplayConfig() WebhookReplayConfig {
	return WebhookReplayConfig{
		WindowSize:  5 * time.Minute,
		MaxNonceAge: 5 * time.Minute,
	}
}

// ProtectedWebhookPayload represents a validated webhook payload
type ProtectedWebhookPayload struct {
	EventID   string
	Nonce     string
	Timestamp int64
	Provider  string
	RawBody   []byte
}

// NewWebhookReplayProtection creates a new webhook replay protection service
func NewWebhookReplayProtection(
	redisClient *redis.Client,
	secrets map[string]string,
	config WebhookReplayConfig,
	logger *zap.Logger,
) *WebhookReplayProtection {
	return &WebhookReplayProtection{
		redis:       redisClient,
		logger:      logger,
		secrets:     secrets,
		windowSize:  config.WindowSize,
		maxNonceAge: config.MaxNonceAge,
	}
}

// ValidateWebhook performs comprehensive webhook validation with replay protection
func (w *WebhookReplayProtection) ValidateWebhook(
	ctx context.Context,
	rawBody []byte,
	signature string,
	provider string,
	eventID string,
	nonce string,
	timestamp int64,
) (*ProtectedWebhookPayload, error) {
	// 1. Validate timestamp
	if err := w.validateTimestamp(timestamp); err != nil {
		w.logger.Warn("Webhook timestamp validation failed",
			zap.String("provider", provider),
			zap.Int64("timestamp", timestamp),
			zap.Error(err))
		return nil, fmt.Errorf("timestamp validation failed: %w", err)
	}

	// 2. Check for duplicate event ID
	if eventID != "" {
		if err := w.checkDuplicateEvent(ctx, provider, eventID); err != nil {
			w.logger.Warn("Duplicate webhook event detected",
				zap.String("provider", provider),
				zap.String("event_id", eventID))
			return nil, err
		}
	}

	// 3. Check for nonce reuse
	if nonce != "" {
		if err := w.checkNonceReuse(ctx, provider, nonce); err != nil {
			w.logger.Warn("Webhook nonce reuse detected",
				zap.String("provider", provider),
				zap.String("nonce", nonce))
			return nil, err
		}
	}

	// 4. Verify signature
	if err := w.verifySignature(rawBody, signature, provider); err != nil {
		w.logger.Warn("Webhook signature verification failed",
			zap.String("provider", provider),
			zap.Error(err))
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	// 5. Store event ID and nonce for future deduplication
	if err := w.storeWebhookData(ctx, provider, eventID, nonce); err != nil {
		w.logger.Error("Failed to store webhook data", zap.Error(err))
		// Don't fail the request, just log
	}

	return &ProtectedWebhookPayload{
		EventID:   eventID,
		Nonce:     nonce,
		Timestamp: timestamp,
		Provider:  provider,
		RawBody:   rawBody,
	}, nil
}

func (w *WebhookReplayProtection) validateTimestamp(timestamp int64) error {
	if timestamp == 0 {
		return nil // Timestamp not provided, skip validation
	}

	eventTime := time.Unix(timestamp, 0)
	now := time.Now()

	// Check if timestamp is too old
	if now.Sub(eventTime) > w.maxNonceAge {
		return fmt.Errorf("webhook timestamp too old: %v (max age: %v)", eventTime, w.maxNonceAge)
	}

	// Check if timestamp is in the future (with small tolerance)
	if eventTime.Sub(now) > w.windowSize {
		return fmt.Errorf("webhook timestamp too far in future: %v", eventTime)
	}

	return nil
}

func (w *WebhookReplayProtection) checkDuplicateEvent(ctx context.Context, provider, eventID string) error {
	key := fmt.Sprintf("webhook:event:%s:%s", provider, eventID)

	exists, err := w.redis.Exists(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to check duplicate event: %w", err)
	}

	if exists > 0 {
		return fmt.Errorf("duplicate webhook event: %s", eventID)
	}

	return nil
}

func (w *WebhookReplayProtection) checkNonceReuse(ctx context.Context, provider, nonce string) error {
	key := fmt.Sprintf("webhook:nonce:%s:%s", provider, nonce)

	exists, err := w.redis.Exists(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to check nonce: %w", err)
	}

	if exists > 0 {
		return fmt.Errorf("webhook nonce already used: %s", nonce)
	}

	return nil
}

func (w *WebhookReplayProtection) verifySignature(payload []byte, signature, provider string) error {
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	secret, ok := w.secrets[provider]
	if !ok {
		return fmt.Errorf("unknown provider: %s", provider)
	}

	// Calculate expected signature
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))

	// Handle common signature prefixes
	sig := signature
	for _, prefix := range []string{"sha256=", "hmac-sha256=", "v1="} {
		if len(sig) > len(prefix) && sig[:len(prefix)] == prefix {
			sig = sig[len(prefix):]
			break
		}
	}

	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}

func (w *WebhookReplayProtection) storeWebhookData(ctx context.Context, provider, eventID, nonce string) error {
	pipe := w.redis.Pipeline()

	if eventID != "" {
		eventKey := fmt.Sprintf("webhook:event:%s:%s", provider, eventID)
		pipe.Set(ctx, eventKey, "1", w.maxNonceAge*2) // Keep for 2x max age
	}

	if nonce != "" {
		nonceKey := fmt.Sprintf("webhook:nonce:%s:%s", provider, nonce)
		pipe.Set(ctx, nonceKey, "1", w.windowSize)
	}

	_, err := pipe.Exec(ctx)
	return err
}

// WebhookIPWhitelist validates webhook source IPs
type WebhookIPWhitelist struct {
	allowedIPs map[string][]string // provider -> allowed CIDRs
	logger     *zap.Logger
}

// NewWebhookIPWhitelist creates a new IP whitelist validator
func NewWebhookIPWhitelist(allowedIPs map[string][]string, logger *zap.Logger) *WebhookIPWhitelist {
	return &WebhookIPWhitelist{
		allowedIPs: allowedIPs,
		logger:     logger,
	}
}

// ValidateIP checks if the client IP is whitelisted for the provider
func (w *WebhookIPWhitelist) ValidateIP(provider, clientIP string) error {
	allowedCIDRs, exists := w.allowedIPs[provider]
	if !exists {
		return nil // No whitelist configured, allow all
	}

	if len(allowedCIDRs) == 0 {
		return nil // Empty whitelist, allow all
	}

	ip := net.ParseIP(clientIP)
	if ip == nil {
		return fmt.Errorf("invalid IP address: %s", clientIP)
	}

	for _, cidr := range allowedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Try parsing as single IP
			if allowedIP := net.ParseIP(cidr); allowedIP != nil && allowedIP.Equal(ip) {
				return nil
			}
			continue
		}

		if ipNet.Contains(ip) {
			return nil
		}
	}

	w.logger.Warn("Webhook IP not whitelisted",
		zap.String("provider", provider),
		zap.String("client_ip", clientIP))

	return fmt.Errorf("IP not whitelisted: %s", clientIP)
}

// WebhookRateLimiter provides rate limiting for webhooks
type WebhookRateLimiter struct {
	redis  *redis.Client
	limits map[string]WebhookRateLimit
	logger *zap.Logger
}

// WebhookRateLimit defines rate limit for a provider
type WebhookRateLimit struct {
	MaxRequests int
	Window      time.Duration
}

// NewWebhookRateLimiter creates a new webhook rate limiter
func NewWebhookRateLimiter(redisClient *redis.Client, limits map[string]WebhookRateLimit, logger *zap.Logger) *WebhookRateLimiter {
	return &WebhookRateLimiter{
		redis:  redisClient,
		limits: limits,
		logger: logger,
	}
}

// CheckRateLimit checks if the webhook rate limit is exceeded
func (w *WebhookRateLimiter) CheckRateLimit(ctx context.Context, provider string) (bool, time.Duration, error) {
	limit, exists := w.limits[provider]
	if !exists {
		limit = w.limits["default"]
		if limit.MaxRequests == 0 {
			return true, 0, nil // No limit configured
		}
	}

	windowSeconds := int64(limit.Window.Seconds())
	if windowSeconds == 0 {
		windowSeconds = 60
	}

	key := fmt.Sprintf("webhook:rate:%s:%d", provider, time.Now().Unix()/windowSeconds)

	current, err := w.redis.Incr(ctx, key).Result()
	if err != nil {
		return true, 0, nil // Fail open on Redis error
	}

	if current == 1 {
		w.redis.Expire(ctx, key, limit.Window)
	}

	if current > int64(limit.MaxRequests) {
		resetTime := time.Duration(windowSeconds-(time.Now().Unix()%windowSeconds)) * time.Second
		return false, resetTime, nil
	}

	return true, 0, nil
}

// ExtractWebhookMetadata extracts common webhook metadata from payload
func ExtractWebhookMetadata(rawBody []byte) (eventID, nonce string, timestamp int64) {
	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return "", "", 0
	}

	// Try common field names for event ID
	for _, field := range []string{"event_id", "eventId", "id", "webhook_id", "webhookId"} {
		if v, ok := payload[field].(string); ok && v != "" {
			eventID = v
			break
		}
	}

	// Try common field names for nonce
	for _, field := range []string{"nonce", "idempotency_key", "idempotencyKey"} {
		if v, ok := payload[field].(string); ok && v != "" {
			nonce = v
			break
		}
	}

	// Try common field names for timestamp
	for _, field := range []string{"timestamp", "created_at", "createdAt", "time"} {
		switch v := payload[field].(type) {
		case float64:
			timestamp = int64(v)
		case int64:
			timestamp = v
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				timestamp = t.Unix()
			}
		}
		if timestamp > 0 {
			break
		}
	}

	return eventID, nonce, timestamp
}

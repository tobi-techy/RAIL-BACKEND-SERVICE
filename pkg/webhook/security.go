package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SignatureValidator validates webhook signatures using HMAC-SHA256
type SignatureValidator struct {
	secret []byte
}

// NewSignatureValidator creates a new signature validator with the given secret
func NewSignatureValidator(secret string) *SignatureValidator {
	return &SignatureValidator{
		secret: []byte(secret),
	}
}

// ValidateSignature validates an HMAC-SHA256 signature against the payload
func (v *SignatureValidator) ValidateSignature(payload []byte, signature string) error {
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	// Remove common prefixes if present
	signature = strings.TrimPrefix(signature, "sha256=")
	signature = strings.TrimPrefix(signature, "hmac-sha256=")

	// Calculate expected signature
	expectedSignature := v.calculateSignature(payload)

	// Compare signatures using constant-time comparison to prevent timing attacks
	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// calculateSignature computes HMAC-SHA256 signature for the payload
func (v *SignatureValidator) calculateSignature(payload []byte) string {
	h := hmac.New(sha256.New, v.secret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// GenerateSignature generates an HMAC-SHA256 signature for testing purposes
func (v *SignatureValidator) GenerateSignature(payload []byte) string {
	return "sha256=" + v.calculateSignature(payload)
}

// ValidateTimestamp checks if the webhook timestamp is within acceptable bounds
// This helps prevent replay attacks
func ValidateTimestamp(timestamp int64, maxAge int64) error {
	if timestamp <= 0 {
		return fmt.Errorf("invalid timestamp")
	}

	currentTime := getCurrentTimestamp()
	if timestamp > currentTime {
		return fmt.Errorf("timestamp is in the future")
	}

	if currentTime-timestamp > maxAge {
		return fmt.Errorf("timestamp is too old")
	}

	return nil
}

// getCurrentTimestamp returns current Unix timestamp
func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

// WebhookSecurityConfig holds configuration for webhook security
type WebhookSecurityConfig struct {
	Secret            string   // HMAC secret key
	MaxTimestampAge   int64    // Maximum age of timestamp in seconds (e.g., 300 for 5 minutes)
	RequireSignature  bool     // Whether signature is required
	RequireTimestamp  bool     // Whether timestamp validation is required
	TrustedUserAgents []string // List of trusted user agents (optional)
	MaxPayloadSize    int64    // Maximum payload size in bytes
}

// DefaultWebhookConfig returns a secure default configuration
func DefaultWebhookConfig() WebhookSecurityConfig {
	return WebhookSecurityConfig{
		Secret:           "",  // Must be provided
		MaxTimestampAge:  300, // 5 minutes
		RequireSignature: true,
		RequireTimestamp: false,       // Set to true if webhooks include timestamp
		MaxPayloadSize:   1024 * 1024, // 1MB
	}
}

// WebhookValidator provides comprehensive webhook validation
type WebhookValidator struct {
	config       WebhookSecurityConfig
	sigValidator *SignatureValidator
}

// NewWebhookValidator creates a new webhook validator
func NewWebhookValidator(config WebhookSecurityConfig) *WebhookValidator {
	var sigValidator *SignatureValidator
	if config.RequireSignature && config.Secret != "" {
		sigValidator = NewSignatureValidator(config.Secret)
	}

	return &WebhookValidator{
		config:       config,
		sigValidator: sigValidator,
	}
}

// ValidateRequest performs comprehensive webhook request validation
func (v *WebhookValidator) ValidateRequest(payload []byte, signature string, timestamp int64, userAgent string) error {
	// Check payload size
	if v.config.MaxPayloadSize > 0 && int64(len(payload)) > v.config.MaxPayloadSize {
		return fmt.Errorf("payload too large: %d bytes (max: %d)", len(payload), v.config.MaxPayloadSize)
	}

	// Validate signature if required
	if v.config.RequireSignature {
		if v.sigValidator == nil {
			return fmt.Errorf("signature validation required but no secret configured")
		}
		if err := v.sigValidator.ValidateSignature(payload, signature); err != nil {
			return fmt.Errorf("signature validation failed: %w", err)
		}
	}

	// Validate timestamp if required
	if v.config.RequireTimestamp {
		if err := ValidateTimestamp(timestamp, v.config.MaxTimestampAge); err != nil {
			return fmt.Errorf("timestamp validation failed: %w", err)
		}
	}

	// Validate user agent if configured
	if len(v.config.TrustedUserAgents) > 0 {
		trusted := false
		for _, trustedUA := range v.config.TrustedUserAgents {
			if strings.Contains(userAgent, trustedUA) {
				trusted = true
				break
			}
		}
		if !trusted {
			return fmt.Errorf("untrusted user agent: %s", userAgent)
		}
	}

	return nil
}

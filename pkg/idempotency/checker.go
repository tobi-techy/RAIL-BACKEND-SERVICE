package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultTTL is the default time-to-live for idempotency keys (24 hours)
	DefaultTTL = 24 * time.Hour
	
	// MaxTTL is the maximum allowed TTL for idempotency keys (7 days)
	MaxTTL = 7 * 24 * time.Hour
)

// Request represents the idempotent request details
type Request struct {
	IdempotencyKey string
	Path           string
	Method         string
	Body           []byte
	UserID         *uuid.UUID
	TTL            time.Duration
}

// Response represents a cached response
type Response struct {
	Status int
	Body   json.RawMessage
}

// HashRequest creates a SHA-256 hash of the request body
func HashRequest(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

// ValidateKey validates an idempotency key format
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("idempotency key cannot be empty")
	}
	
	if len(key) < 16 {
		return fmt.Errorf("idempotency key must be at least 16 characters")
	}
	
	if len(key) > 255 {
		return fmt.Errorf("idempotency key must not exceed 255 characters")
	}
	
	// Check for valid characters (alphanumeric, dash, underscore)
	for _, c := range key {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || 
		     (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return fmt.Errorf("idempotency key contains invalid character: %c", c)
		}
	}
	
	return nil
}

// ValidateTTL validates and normalizes the TTL
func ValidateTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		return DefaultTTL, nil
	}
	
	if ttl < time.Minute {
		return 0, fmt.Errorf("TTL must be at least 1 minute")
	}
	
	if ttl > MaxTTL {
		return 0, fmt.Errorf("TTL cannot exceed %v", MaxTTL)
	}
	
	return ttl, nil
}

// ReadBody reads the request body and returns it for later use
func ReadBody(body io.ReadCloser, maxSize int64) ([]byte, error) {
	// Limit the body size to prevent memory exhaustion
	if maxSize == 0 {
		maxSize = 10 << 20 // 10MB default
	}
	
	limitedReader := io.LimitReader(body, maxSize)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	
	return bodyBytes, nil
}

// CompareRequestHash compares the current request hash with stored hash
func CompareRequestHash(currentBody []byte, storedHash string) bool {
	currentHash := HashRequest(currentBody)
	return currentHash == storedHash
}

// ShouldReturnCached determines if a cached response should be returned
func ShouldReturnCached(stored *Response, currentHash, storedHash string) (bool, string) {
	// If hashes don't match, request body has changed
	if currentHash != storedHash {
		return false, "request body differs from original"
	}
	
	// If response was a success (2xx) or client error (4xx), return cached
	if stored.Status >= 200 && stored.Status < 300 {
		return true, "returning cached success response"
	}
	
	if stored.Status >= 400 && stored.Status < 500 {
		return true, "returning cached client error response"
	}
	
	// For server errors (5xx), allow retry
	return false, "allowing retry for server error"
}

// GenerateKey generates a new idempotency key
func GenerateKey() string {
	return fmt.Sprintf("idem_%s", uuid.New().String())
}

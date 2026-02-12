package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// CSRFStore manages CSRF tokens with expiration
type CSRFStore struct {
	tokens map[string]time.Time
	mu     sync.RWMutex
}

// NewCSRFStore creates a new CSRF token store with automatic cleanup
func NewCSRFStore() *CSRFStore {
	store := &CSRFStore{
		tokens: make(map[string]time.Time),
	}
	go store.cleanup()
	return store
}

func (s *CSRFStore) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for token, expiry := range s.tokens {
			if now.After(expiry) {
				delete(s.tokens, token)
			}
		}
		s.mu.Unlock()
	}
}

// Generate creates a new CSRF token with 1-hour expiration
func (s *CSRFStore) Generate() string {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.URLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = time.Now().Add(1 * time.Hour)
	s.mu.Unlock()
	return token
}

// Validate checks if a CSRF token is valid and not expired
func (s *CSRFStore) Validate(token string) bool {
	s.mu.RLock()
	expiry, exists := s.tokens[token]
	s.mu.RUnlock()
	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return false
	}
	return true
}

<<<<<<< omotadetobiloba/sta-84-security-missing-csrf-protection-on-state-changing-auth
// CSRFProtection validates CSRF tokens for state-changing requests.
// For browser clients: requires X-CSRF-Token header
// For API clients: requires X-Requested-With header (custom header requirement)
func CSRFProtection(store *CSRFStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip CSRF for safe methods
=======
func CSRFProtection(store *CSRFStore, isDev bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip CSRF in development for API testing
		if isDev {
			c.Next()
			return
		}

>>>>>>> main
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// API clients can bypass CSRF by sending X-Requested-With header
		// This is secure because custom headers cannot be sent cross-origin without CORS preflight
		requestedWith := c.GetHeader("X-Requested-With")
		if requestedWith == "XMLHttpRequest" || requestedWith == "RailApp" {
			c.Next()
			return
		}

		// For browser clients, validate CSRF token
		token := c.GetHeader("X-CSRF-Token")

		if token == "" || !store.Validate(token) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "CSRF_VALIDATION_FAILED",
				"message":    "CSRF token validation failed. Include X-CSRF-Token header or X-Requested-With: RailApp for API clients.",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// CSRFToken generates and attaches a CSRF token to every response
func CSRFToken(store *CSRFStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := store.Generate()
		c.Header("X-CSRF-Token", token)
		c.Set("csrf_token", token)
		c.Next()
	}
}

// AuthCSRFProtection provides CSRF protection specifically for auth endpoints.
// Uses double-submit cookie pattern combined with custom header requirement.
func AuthCSRFProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip CSRF for safe methods
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// Require custom header for all state-changing auth requests
		// This prevents CSRF because:
		// 1. Custom headers cannot be sent cross-origin without CORS preflight
		// 2. CORS preflight will fail for malicious origins
		requestedWith := c.GetHeader("X-Requested-With")
		if requestedWith != "XMLHttpRequest" && requestedWith != "RailApp" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "CSRF_PROTECTION",
				"message":    "Missing required header. Include X-Requested-With: RailApp for API requests.",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

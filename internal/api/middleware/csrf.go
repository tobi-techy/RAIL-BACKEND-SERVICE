package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type CSRFStore struct {
	tokens map[string]time.Time
	mu     sync.RWMutex
}

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

func (s *CSRFStore) Generate() string {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.URLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = time.Now().Add(1 * time.Hour)
	s.mu.Unlock()
	return token
}

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

func CSRFProtection(store *CSRFStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		token := c.GetHeader("X-CSRF-Token")

		if token == "" || !store.Validate(token) {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "CSRF token validation failed",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func CSRFToken(store *CSRFStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := store.Generate()
		c.Header("X-CSRF-Token", token)
		c.Set("csrf_token", token)
		c.Next()
	}
}

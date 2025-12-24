package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthRateLimiter_AllowsWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewAuthRateLimiter(5)

	router := gin.New()
	router.Use(limiter.Limit())
	router.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Should allow 5 requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "Request %d should be allowed", i+1)
	}
}

func TestAuthRateLimiter_BlocksExcessRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewAuthRateLimiter(3)

	router := gin.New()
	router.Use(limiter.Limit())
	router.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Exhaust the limit
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}

	// Next request should be blocked
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "RATE_LIMIT_EXCEEDED")
}

func TestAuthRateLimiter_SeparateLimitsPerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewAuthRateLimiter(2)

	router := gin.New()
	router.Use(limiter.Limit())
	router.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Exhaust limit for IP1
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}

	// IP2 should still be allowed
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

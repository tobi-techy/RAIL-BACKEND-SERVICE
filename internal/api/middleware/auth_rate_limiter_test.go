package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthRateLimiter_AllowsWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewAuthRateLimiter(5)
	defer limiter.Stop()

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
	defer limiter.Stop()

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
	defer limiter.Stop()

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

func TestAuthRateLimiter_ZeroRequestsPerMinute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Should not panic with zero or negative values
	limiter := NewAuthRateLimiter(0)
	defer limiter.Stop()
	assert.NotNil(t, limiter)

	// Test actual behavior with zero limit
	router := gin.New()
	router.Use(limiter.Limit())
	router.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Assert expected behavior - should this block all requests or allow all?
	// Example: assert.Equal(t, http.StatusTooManyRequests, w.Code)

	limiter2 := NewAuthRateLimiter(-5)
	defer limiter2.Stop()
	assert.NotNil(t, limiter2)

	// Test actual behavior with negative limit
	router2 := gin.New()
	router2.Use(limiter2.Limit())
	router2.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	req2 := httptest.NewRequest(http.MethodPost, "/login", nil)
	req2.RemoteAddr = "192.168.1.1:12345"
	w2 := httptest.NewRecorder()
	router2.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestAuthRateLimiter_TTLCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create limiter with very short TTL for testing
	limiter := NewAuthRateLimiterWithTTL(10, 100*time.Millisecond)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Limit())
	router.POST("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Make a request to create an entry
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify entry exists
	assert.Equal(t, 1, limiter.Size())

	// Wait for cleanup to run (TTL + cleanup interval buffer)
	time.Sleep(300 * time.Millisecond)

	// Force cleanup
	limiter.cleanup()

	// Entry should be removed
	assert.Equal(t, 0, limiter.Size())
}

func TestAuthRateLimiter_Size(t *testing.T) {
	limiter := NewAuthRateLimiter(10)
	defer limiter.Stop()

	assert.Equal(t, 0, limiter.Size())

	// Access creates entries
	limiter.getLimiter("ip1")
	assert.Equal(t, 1, limiter.Size())

	limiter.getLimiter("ip2")
	assert.Equal(t, 2, limiter.Size())

	// Same IP doesn't create new entry
	limiter.getLimiter("ip1")
	assert.Equal(t, 2, limiter.Size())
}

func TestAuthRateLimiter_StopIdempotent(t *testing.T) {
	limiter := NewAuthRateLimiter(10)

	// Stop should be safe to call multiple times
	limiter.Stop()
	limiter.Stop()
	limiter.Stop()
}

func TestAuthRateLimit_Singleton(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Reset singleton before test
	ResetAuthRateLimiter()
	defer ResetAuthRateLimiter()

	// First call creates the singleton
	middleware1 := AuthRateLimit(5)
	assert.NotNil(t, middleware1)

	instance1 := GetAuthRateLimiter()
	assert.NotNil(t, instance1)

	// Second call returns the same instance
	middleware2 := AuthRateLimit(10) // Different rate, but should return same instance
	assert.NotNil(t, middleware2)

	instance2 := GetAuthRateLimiter()
	assert.Same(t, instance1, instance2, "Should return same singleton instance")
}

func TestAuthRateLimiter_RaceConditionHandling(t *testing.T) {
	// Test that getLimiter handles the case where entry is deleted between read and write
	limiter := NewAuthRateLimiterWithTTL(10, 50*time.Millisecond)
	defer limiter.Stop()

	// Create an entry
	l1 := limiter.getLimiter("test-ip")
	assert.NotNil(t, l1)
	assert.Equal(t, 1, limiter.Size())

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Force cleanup to delete the entry
	limiter.cleanup()
	assert.Equal(t, 0, limiter.Size())

	// Get limiter again - should create new one without panic
	l2 := limiter.getLimiter("test-ip")
	assert.NotNil(t, l2)
	assert.Equal(t, 1, limiter.Size())
}

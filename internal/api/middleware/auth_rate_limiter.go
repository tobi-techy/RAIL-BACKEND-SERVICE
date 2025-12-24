package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// AuthRateLimiter provides stricter rate limiting for authentication endpoints
type AuthRateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

// NewAuthRateLimiter creates a rate limiter for auth endpoints
// requestsPerMinute: max requests allowed per minute per IP
func NewAuthRateLimiter(requestsPerMinute int) *AuthRateLimiter {
	return &AuthRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Every(time.Minute / time.Duration(requestsPerMinute)),
		burst:    requestsPerMinute,
	}
}

func (al *AuthRateLimiter) getLimiter(key string) *rate.Limiter {
	al.mu.RLock()
	limiter, exists := al.limiters[key]
	al.mu.RUnlock()

	if exists {
		return limiter
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = al.limiters[key]; exists {
		return limiter
	}

	limiter = rate.NewLimiter(al.rate, al.burst)
	al.limiters[key] = limiter
	return limiter
}

// Limit returns middleware that rate limits by IP
func (al *AuthRateLimiter) Limit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !al.getLimiter(ip).Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "RATE_LIMIT_EXCEEDED",
				"message":     "Too many requests. Please try again later.",
				"retry_after": 60,
				"request_id":  c.GetString("request_id"),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// AuthRateLimit creates middleware with specified requests per minute
func AuthRateLimit(requestsPerMinute int) gin.HandlerFunc {
	limiter := NewAuthRateLimiter(requestsPerMinute)
	return limiter.Limit()
}

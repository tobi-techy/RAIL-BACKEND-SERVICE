package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// Default cleanup interval and TTL for rate limiter entries
const (
	defaultCleanupInterval = 5 * time.Minute
	defaultCleanupTTL      = 10 * time.Minute
)

// limiterEntry stores a rate limiter with its last access time for TTL-based cleanup
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// AuthRateLimiter provides stricter rate limiting for authentication endpoints
// with TTL-based cleanup to prevent unbounded memory growth
type AuthRateLimiter struct {
	limiters   map[string]*limiterEntry
	mu         sync.RWMutex
	rate       rate.Limit
	burst      int
	cleanupTTL time.Duration
	stopCh     chan struct{}
}

// NewAuthRateLimiter creates a rate limiter for auth endpoints
// requestsPerMinute: max requests allowed per minute per IP
// Input validation: clamps requestsPerMinute to minimum of 1 to prevent division by zero
func NewAuthRateLimiter(requestsPerMinute int) *AuthRateLimiter {
	// Clamp to minimum of 1 to prevent division by zero and ensure valid burst
	if requestsPerMinute <= 0 {
		requestsPerMinute = 1
	}

	al := &AuthRateLimiter{
		limiters:   make(map[string]*limiterEntry),
		rate:       rate.Every(time.Minute / time.Duration(requestsPerMinute)),
		burst:      requestsPerMinute,
		cleanupTTL: defaultCleanupTTL,
		stopCh:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go al.cleanupLoop(defaultCleanupInterval)

	return al
}

// NewAuthRateLimiterWithTTL creates a rate limiter with custom cleanup TTL
func NewAuthRateLimiterWithTTL(requestsPerMinute int, cleanupTTL time.Duration) *AuthRateLimiter {
	if requestsPerMinute <= 0 {
		requestsPerMinute = 1
	}
	if cleanupTTL <= 0 {
		cleanupTTL = defaultCleanupTTL
	}

	al := &AuthRateLimiter{
		limiters:   make(map[string]*limiterEntry),
		rate:       rate.Every(time.Minute / time.Duration(requestsPerMinute)),
		burst:      requestsPerMinute,
		cleanupTTL: cleanupTTL,
		stopCh:     make(chan struct{}),
	}

	go al.cleanupLoop(defaultCleanupInterval)

	return al
}

// cleanupLoop periodically removes stale entries from the limiters map
func (al *AuthRateLimiter) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			al.cleanup()
		case <-al.stopCh:
			return
		}
	}
}

// cleanup removes entries that haven't been accessed within the TTL
func (al *AuthRateLimiter) cleanup() {
	al.mu.Lock()
	defer al.mu.Unlock()

	now := time.Now()
	for key, entry := range al.limiters {
		if now.Sub(entry.lastSeen) > al.cleanupTTL {
			delete(al.limiters, key)
		}
	}
}

// Stop stops the background cleanup goroutine
func (al *AuthRateLimiter) Stop() {
	close(al.stopCh)
}

// getLimiter returns the rate limiter for the given key, creating one if needed
// Updates lastSeen time on each access
func (al *AuthRateLimiter) getLimiter(key string) *rate.Limiter {
	now := time.Now()

	al.mu.RLock()
	entry, exists := al.limiters[key]
	al.mu.RUnlock()

	if exists {
		// Update lastSeen time
		al.mu.Lock()
		if entry, exists = al.limiters[key]; exists {
			entry.lastSeen = now
		}
		al.mu.Unlock()
		return entry.limiter
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists = al.limiters[key]; exists {
		entry.lastSeen = now
		return entry.limiter
	}

	limiter := rate.NewLimiter(al.rate, al.burst)
	al.limiters[key] = &limiterEntry{
		limiter:  limiter,
		lastSeen: now,
	}
	return limiter
}

// getClientIP safely extracts the client IP address
// Note: c.ClientIP() trusts X-Forwarded-For headers. For production use,
// ensure Gin trusted proxies are configured via engine.SetTrustedProxies()
// with your actual proxy IPs (e.g., []string{"10.0.0.0/8"} for internal proxies).
// If no trusted proxies are configured, this falls back to RemoteAddr.
func getClientIP(c *gin.Context) string {
	// c.ClientIP() is safe when trusted proxies are properly configured
	// The application should call engine.SetTrustedProxies() during initialization
	// with the appropriate proxy IPs to prevent IP spoofing attacks
	return c.ClientIP()
}

// Limit returns middleware that rate limits by IP
func (al *AuthRateLimiter) Limit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := getClientIP(c)
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

// Size returns the current number of entries in the limiter map (for testing/monitoring)
func (al *AuthRateLimiter) Size() int {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return len(al.limiters)
}

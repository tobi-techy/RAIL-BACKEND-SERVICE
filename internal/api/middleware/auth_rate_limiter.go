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

// Package-level singleton for AuthRateLimit middleware
var (
	authRateLimiterInstance *AuthRateLimiter
	authRateLimiterOnce     sync.Once
	authRateLimiterMu       sync.Mutex
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
	stopped    bool
}

// NewAuthRateLimiter creates a rate limiter for auth endpoints.
// requestsPerMinute: max requests allowed per minute per IP.
// Input validation: clamps requestsPerMinute to minimum of 1 to prevent division by zero.
// Note: Each call creates a new instance with its own cleanup goroutine.
// For middleware use, prefer AuthRateLimit() which uses a singleton.
func NewAuthRateLimiter(requestsPerMinute int) *AuthRateLimiter {
	return newAuthRateLimiter(requestsPerMinute, defaultCleanupTTL)
}

// NewAuthRateLimiterWithTTL creates a rate limiter with custom cleanup TTL.
// Note: Each call creates a new instance with its own cleanup goroutine.
// Caller must call Stop() to prevent goroutine leaks.
func NewAuthRateLimiterWithTTL(requestsPerMinute int, cleanupTTL time.Duration) *AuthRateLimiter {
	if cleanupTTL <= 0 {
		cleanupTTL = defaultCleanupTTL
	}
	return newAuthRateLimiter(requestsPerMinute, cleanupTTL)
}

// newAuthRateLimiter is the internal constructor
func newAuthRateLimiter(requestsPerMinute int, cleanupTTL time.Duration) *AuthRateLimiter {
	// Clamp to minimum of 1 to prevent division by zero and ensure valid burst
	if requestsPerMinute <= 0 {
		requestsPerMinute = 1
	}

	al := &AuthRateLimiter{
		limiters:   make(map[string]*limiterEntry),
		rate:       rate.Every(time.Minute / time.Duration(requestsPerMinute)),
		burst:      requestsPerMinute,
		cleanupTTL: cleanupTTL,
		stopCh:     make(chan struct{}),
		stopped:    false,
	}

	// Start background cleanup goroutine
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

// Stop stops the background cleanup goroutine.
// Safe to call multiple times.
func (al *AuthRateLimiter) Stop() {
	al.mu.Lock()
	if !al.stopped {
		al.stopped = true
		close(al.stopCh)
	}
	al.mu.Unlock()
}

// getLimiter returns the rate limiter for the given key, creating one if needed.
// Updates lastSeen time on each access.
// Handles race condition where entry may be deleted between read and write locks.
func (al *AuthRateLimiter) getLimiter(key string) *rate.Limiter {
	now := time.Now()

	// Fast path: check if entry exists with read lock
	al.mu.RLock()
	entry, exists := al.limiters[key]
	al.mu.RUnlock()

	if exists {
		// Entry existed during read, but may have been cleaned up.
		// Acquire write lock and re-verify before updating.
		al.mu.Lock()
		entry, exists = al.limiters[key]
		if exists {
			// Entry still exists, update lastSeen and return
			entry.lastSeen = now
			al.mu.Unlock()
			return entry.limiter
		}
		// Entry was deleted between read and write lock - fall through to create new one
		al.mu.Unlock()
	}

	// Slow path: create new entry with write lock
	al.mu.Lock()
	defer al.mu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have created it)
	if entry, exists = al.limiters[key]; exists {
		entry.lastSeen = now
		return entry.limiter
	}

	// Create new limiter
	limiter := rate.NewLimiter(al.rate, al.burst)
	al.limiters[key] = &limiterEntry{
		limiter:  limiter,
		lastSeen: now,
	}
	return limiter
}

// getClientIP safely extracts the client IP address.
// Note: c.ClientIP() trusts X-Forwarded-For headers. For production use,
// ensure Gin trusted proxies are configured via engine.SetTrustedProxies()
// with your actual proxy IPs (e.g., []string{"10.0.0.0/8"} for internal proxies).
func getClientIP(c *gin.Context) string {
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

// AuthRateLimit creates middleware with specified requests per minute.
// This function uses a singleton pattern - the first call creates the limiter,
// subsequent calls return the same instance. This prevents goroutine leaks
// from multiple cleanup goroutines.
//
// To stop the singleton (e.g., in tests or shutdown), call StopAuthRateLimiter().
func AuthRateLimit(requestsPerMinute int) gin.HandlerFunc {
	authRateLimiterOnce.Do(func() {
		authRateLimiterInstance = NewAuthRateLimiter(requestsPerMinute)
	})
	return authRateLimiterInstance.Limit()
}

// GetAuthRateLimiter returns the singleton AuthRateLimiter instance, or nil if not initialized.
func GetAuthRateLimiter() *AuthRateLimiter {
	authRateLimiterMu.Lock()
	defer authRateLimiterMu.Unlock()
	return authRateLimiterInstance
}

// StopAuthRateLimiter stops the singleton AuthRateLimiter's cleanup goroutine.
// Should be called during application shutdown or in test cleanup.
func StopAuthRateLimiter() {
	authRateLimiterMu.Lock()
	defer authRateLimiterMu.Unlock()
	if authRateLimiterInstance != nil {
		authRateLimiterInstance.Stop()
	}
}

// ResetAuthRateLimiter stops and resets the singleton for testing purposes.
// This allows tests to create fresh instances.
func ResetAuthRateLimiter() {
	authRateLimiterMu.Lock()
	defer authRateLimiterMu.Unlock()
	if authRateLimiterInstance != nil {
		authRateLimiterInstance.Stop()
		authRateLimiterInstance = nil
	}
	authRateLimiterOnce = sync.Once{}
}

// Size returns the current number of entries in the limiter map (for testing/monitoring)
func (al *AuthRateLimiter) Size() int {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return len(al.limiters)
}

package ratelimit

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// fallbackLimiters provides in-memory rate limiting when Redis is unavailable.
var fallbackLimiters sync.Map

// getFallbackLimiter returns a per-key in-memory rate limiter (10 req/s, burst 20).
func getFallbackLimiter(key string) *rate.Limiter {
	if v, ok := fallbackLimiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(10, 20)
	actual, _ := fallbackLimiters.LoadOrStore(key, l)
	return actual.(*rate.Limiter)
}

// KeyFunc extracts the rate limit key from the request
type KeyFunc func(*gin.Context) string

// MiddlewareOptions holds configuration for rate limiting middleware
type MiddlewareOptions struct {
	Limiter  Limiter
	KeyFunc  KeyFunc
	Logger   *zap.Logger
	FailOpen bool // If true, allow requests when rate limiter fails. For sensitive endpoints, set to false.
}

// Middleware creates a rate limiting middleware
func Middleware(opts MiddlewareOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := opts.KeyFunc(c)
		if key == "" {
			if opts.Logger != nil {
				opts.Logger.Warn("Rate limit key is empty, allowing request")
			}
			c.Next()
			return
		}

		allowed, err := opts.Limiter.Allow(c.Request.Context(), key)
		if err != nil {
			if opts.Logger != nil {
				opts.Logger.Error("Rate limit check failed, falling back to in-memory limiter",
					zap.Error(err),
					zap.String("key", key))
			}
			if opts.FailOpen {
				// Fall back to in-memory rate limiter instead of allowing unconditionally
				if !getFallbackLimiter(key).Allow() {
					c.JSON(http.StatusTooManyRequests, gin.H{
						"error":   "rate_limit_exceeded",
						"message": "Too many requests, please try again later",
					})
					c.Abort()
					return
				}
				c.Next()
				return
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "rate_limit_unavailable",
				"message": "Service temporarily unavailable",
			})
			c.Abort()
			return
		}

		if !allowed {
			if opts.Logger != nil {
				opts.Logger.Warn("Rate limit exceeded",
					zap.String("key", key),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method))
			}

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": "Too many requests, please try again later",
			})
			c.Abort()
			return
		}

		remaining, err := opts.Limiter.GetRemaining(c.Request.Context(), key)
		if err == nil {
			c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		}

		c.Next()
	}
}

// MiddlewareWithDefaults creates a rate limiting middleware with default settings (fail-open for non-sensitive endpoints)
func MiddlewareWithDefaults(limiter Limiter, keyFunc KeyFunc, logger *zap.Logger) gin.HandlerFunc {
	return Middleware(MiddlewareOptions{
		Limiter:  limiter,
		KeyFunc:  keyFunc,
		Logger:   logger,
		FailOpen: true,
	})
}

// StrictMiddleware creates a rate limiting middleware that fails-closed for sensitive endpoints
func StrictMiddleware(limiter Limiter, keyFunc KeyFunc, logger *zap.Logger) gin.HandlerFunc {
	return Middleware(MiddlewareOptions{
		Limiter:  limiter,
		KeyFunc:  keyFunc,
		Logger:   logger,
		FailOpen: false,
	})
}

// UserKeyFunc extracts user ID from context
func UserKeyFunc(c *gin.Context) string {
	userID, exists := c.Get("user_id")
	if !exists {
		return ""
	}

	if id, ok := userID.(string); ok {
		return id
	}

	return ""
}

// IPKeyFunc extracts IP address from request
func IPKeyFunc(c *gin.Context) string {
	return c.ClientIP()
}

// EndpointKeyFunc extracts endpoint from request
func EndpointKeyFunc(c *gin.Context) string {
	return c.Request.Method + ":" + c.Request.URL.Path
}

// CompositeKeyFunc combines multiple key functions
func CompositeKeyFunc(funcs ...KeyFunc) KeyFunc {
	return func(c *gin.Context) string {
		keys := ""
		for i, fn := range funcs {
			key := fn(c)
			if key == "" {
				return ""
			}
			if i > 0 {
				keys += ":"
			}
			keys += key
		}
		return keys
	}
}

// UserAndEndpointKeyFunc combines user ID and endpoint
func UserAndEndpointKeyFunc(c *gin.Context) string {
	return CompositeKeyFunc(UserKeyFunc, EndpointKeyFunc)(c)
}

// IPAndEndpointKeyFunc combines IP and endpoint
func IPAndEndpointKeyFunc(c *gin.Context) string {
	return CompositeKeyFunc(IPKeyFunc, EndpointKeyFunc)(c)
}

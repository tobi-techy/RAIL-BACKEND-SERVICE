package ratelimit

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// KeyFunc extracts the rate limit key from the request
type KeyFunc func(*gin.Context) string

// Middleware creates a rate limiting middleware
func Middleware(limiter Limiter, keyFunc KeyFunc, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract key
		key := keyFunc(c)
		if key == "" {
			logger.Warn("Rate limit key is empty, allowing request")
			c.Next()
			return
		}
		
		// Check rate limit
		allowed, err := limiter.Allow(c.Request.Context(), key)
		if err != nil {
			logger.Error("Rate limit check failed",
				zap.Error(err),
				zap.String("key", key))
			// Fail open on errors
			c.Next()
			return
		}
		
		if !allowed {
			logger.Warn("Rate limit exceeded",
				zap.String("key", key),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method))
			
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": "Too many requests, please try again later",
			})
			c.Abort()
			return
		}
		
		// Add remaining quota to response headers
		remaining, err := limiter.GetRemaining(c.Request.Context(), key)
		if err == nil {
			c.Header("X-RateLimit-Remaining", string(rune(remaining)))
		}
		
		c.Next()
	}
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

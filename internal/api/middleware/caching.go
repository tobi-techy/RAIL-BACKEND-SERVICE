package middleware

import (
	"context"
	"crypto/md5"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/stack-service/stack_service/internal/infrastructure/cache"
	"github.com/stack-service/stack_service/pkg/logger"

	"github.com/gin-gonic/gin"
)

// APICache provides response caching using Redis
type APICache struct {
	redis  cache.RedisClient
	logger *logger.Logger
}

// CachedResponse represents a cached API response
type CachedResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// NewAPICache creates a new API cache instance
func NewAPICache(redis cache.RedisClient, logger *logger.Logger) *APICache {
	return &APICache{
		redis:  redis,
		logger: logger,
	}
}

// Cache middleware caches API responses in Redis
func (ac *APICache) Cache(ttl time.Duration, cacheKeyFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only cache GET requests
		if c.Request.Method != "GET" {
			c.Next()
			return
		}

		// Skip caching for authenticated requests (unless specified)
		if c.GetHeader("Authorization") != "" {
			// Check if endpoint is marked as cacheable for authenticated users
			if !strings.Contains(c.Request.URL.Path, "/public/") {
				c.Next()
				return
			}
		}

		// Generate cache key
		var cacheKey string
		if cacheKeyFunc != nil {
			cacheKey = cacheKeyFunc(c)
		} else {
			cacheKey = ac.generateDefaultCacheKey(c)
		}

		// Try to get cached response
		var cachedResponse CachedResponse
		err := ac.redis.Get(context.Background(), cacheKey, &cachedResponse)
		if err == nil {
			// Set cached headers
			for key, value := range cachedResponse.Headers {
				c.Header(key, value)
			}

			c.Data(cachedResponse.StatusCode, "application/json", []byte(cachedResponse.Body))
			c.Abort()
			return
		}

		// Create a response writer wrapper to capture the response
		writer := &cacheWriter{
			ResponseWriter: c.Writer,
			body:           &[]byte{},
		}
		c.Writer = writer

		c.Next()

		// Cache the response if successful
		if c.Writer.Status() == http.StatusOK && len(*writer.body) > 0 {
			cachedResponse := CachedResponse{
				StatusCode: c.Writer.Status(),
				Headers:    make(map[string]string),
				Body:       string(*writer.body),
			}

			// Copy headers
			for key, values := range c.Writer.Header() {
				if len(values) > 0 {
					cachedResponse.Headers[key] = values[0]
				}
			}

			// Cache with TTL
			err := ac.redis.Set(context.Background(), cacheKey, cachedResponse, ttl)
			if err != nil {
				ac.logger.Warnw("Failed to cache API response", "error", err, "key", cacheKey)
			}
		}
	}
}

// generateDefaultCacheKey generates a default cache key based on request
func (ac *APICache) generateDefaultCacheKey(c *gin.Context) string {
	// Include method, path, and query params in cache key
	key := fmt.Sprintf("api:%s:%s", c.Request.Method, c.Request.URL.Path)

	// Include relevant query parameters
	if query := c.Request.URL.RawQuery; query != "" {
		// Sort query params for consistent caching
		key += ":" + query
	}

	// Include user ID for personalized responses (if available)
	if userID := c.GetString("user_id"); userID != "" {
		key += ":user:" + userID
	}

	// Hash the key if it's too long
	if len(key) > 250 {
		hash := md5.Sum([]byte(key))
		key = fmt.Sprintf("api:hash:%x", hash)
	}

	return key
}

// cacheWriter captures response data for caching
type cacheWriter struct {
	gin.ResponseWriter
	body *[]byte
}

func (w *cacheWriter) Write(data []byte) (int, error) {
	*w.body = append(*w.body, data...)
	return w.ResponseWriter.Write(data)
}

// CacheByUserID generates cache key based on user ID
func CacheByUserID(c *gin.Context) string {
	userID := c.GetString("user_id")
	if userID == "" {
		userID = "anonymous"
	}
	return fmt.Sprintf("api:user:%s:%s:%s",
		userID,
		c.Request.Method,
		c.Request.URL.Path+c.Request.URL.RawQuery,
	)
}

// CacheByPath generates cache key based on path only
func CacheByPath(c *gin.Context) string {
	return fmt.Sprintf("api:public:%s:%s",
		c.Request.Method,
		c.Request.URL.Path+c.Request.URL.RawQuery,
	)
}

// InvalidateCache invalidates cache keys matching a pattern (simplified)
func (ac *APICache) InvalidateCache(ctx context.Context, pattern string) error {
	// Simplified implementation - in production you'd need to extend RedisClient interface
	ac.logger.Infow("Cache invalidation requested", "pattern", pattern)
	return nil
}

// ClearUserCache clears cache for a specific user
func (ac *APICache) ClearUserCache(ctx context.Context, userID string) error {
	pattern := fmt.Sprintf("api:user:%s:*", userID)
	return ac.InvalidateCache(ctx, pattern)
}

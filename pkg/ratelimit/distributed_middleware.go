package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/metrics"
	"go.uber.org/zap"
)

type DistributedRateLimiter struct {
	limiter  *TieredLimiter
	config   config.RateLimitConfig
	logger   *zap.Logger
	failOpen bool
}

func NewDistributedRateLimiter(limiter *TieredLimiter, cfg config.RateLimitConfig, logger *zap.Logger) *DistributedRateLimiter {
	return &DistributedRateLimiter{
		limiter:  limiter,
		config:   cfg,
		logger:   logger,
		failOpen: cfg.FailOpen,
	}
}

func (rl *DistributedRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.config.Enabled {
			c.Next()
			return
		}

		ip := c.ClientIP()
		userID := ""
		if uid, exists := c.Get("user_id"); exists {
			if id, ok := uid.(string); ok {
				userID = id
			}
		}

		endpoint := c.Request.Method + ":" + c.Request.URL.Path

		var result *CheckResult
		var err error

		if rl.limiter != nil {
			result, err = rl.limiter.Check(c.Request.Context(), ip, userID, endpoint)
			if err != nil {
				rl.logger.Error("Rate limit check failed",
					zap.Error(err),
					zap.String("ip", ip),
					zap.String("endpoint", endpoint))

				if !rl.failOpen {
					c.JSON(http.StatusServiceUnavailable, gin.H{
						"error":      "service_unavailable",
						"message":    "Rate limiting service is temporarily unavailable",
						"request_id": c.GetString("request_id"),
					})
					c.Abort()
					return
				}
				rl.logger.Warn("Rate limit check failed, failing open")
			}
		} else {
			rl.logger.Warn("Rate limiter not configured, allowing request")
		}

		if result != nil && !result.Allowed {
			metrics.RateLimitHitsTotal.WithLabelValues(result.LimitedBy, endpoint).Inc()

			rl.logger.Warn("Rate limit exceeded",
				zap.String("ip", ip),
				zap.String("user_id", userID),
				zap.String("endpoint", endpoint),
				zap.String("limited_by", result.LimitedBy),
				zap.Duration("retry_after", result.RetryAfter))

			headers := gin.H{
				"error":      "rate_limit_exceeded",
				"message":    "Too many requests, please try again later",
				"request_id": c.GetString("request_id"),
			}

			if rl.config.ResponseHeaders {
				headers["X-RateLimit-Limit"] = strconv.FormatInt(rl.getLimit(result.LimitedBy), 10)
				headers["X-RateLimit-Remaining"] = strconv.FormatInt(result.Remaining, 10)
				headers["Retry-After"] = strconv.FormatInt(int64(result.RetryAfter.Seconds()), 10)

				c.Header("X-RateLimit-Limit", strconv.FormatInt(rl.getLimit(result.LimitedBy), 10))
				c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
				c.Header("Retry-After", strconv.FormatInt(int64(result.RetryAfter.Seconds()), 10))
			}

			c.JSON(http.StatusTooManyRequests, headers)
			c.Abort()
			return
		}

		if rl.config.ResponseHeaders && result != nil {
			c.Header("X-RateLimit-Limit", strconv.FormatInt(rl.getLimit(result.LimitedBy), 10))
			c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
		}

		c.Next()
	}
}

func (rl *DistributedRateLimiter) getLimit(tier string) int64 {
	switch tier {
	case "global":
		return rl.config.GlobalLimit
	case "ip":
		return rl.config.IPLimit
	case "user":
		return rl.config.UserLimit
	case "endpoint":
		return rl.config.UserLimit // Default for endpoint-specific limits
	default:
		return rl.config.GlobalLimit
	}
}

func (rl *DistributedRateLimiter) StrictMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.config.Enabled {
			c.Next()
			return
		}

		ip := c.ClientIP()
		userID := ""
		if uid, exists := c.Get("user_id"); exists {
			if id, ok := uid.(string); ok {
				userID = id
			}
		}

		endpoint := c.Request.Method + ":" + c.Request.URL.Path

		var result *CheckResult
		var err error

		if rl.limiter != nil {
			result, err = rl.limiter.Check(c.Request.Context(), ip, userID, endpoint)
			if err != nil {
				rl.logger.Error("Rate limit check failed",
					zap.Error(err),
					zap.String("ip", ip),
					zap.String("endpoint", endpoint))

				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error":      "service_unavailable",
					"message":    "Rate limiting service is temporarily unavailable",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}
		} else {
			rl.logger.Warn("Rate limiter not configured, allowing request")
		}

		if result != nil && !result.Allowed {
			metrics.RateLimitHitsTotal.WithLabelValues(result.LimitedBy, endpoint).Inc()

			rl.logger.Warn("Rate limit exceeded",
				zap.String("ip", ip),
				zap.String("user_id", userID),
				zap.String("endpoint", endpoint),
				zap.String("limited_by", result.LimitedBy))

			headers := gin.H{
				"error":      "rate_limit_exceeded",
				"message":    "Too many requests, please try again later",
				"request_id": c.GetString("request_id"),
			}

			if rl.config.ResponseHeaders {
				c.Header("X-RateLimit-Limit", strconv.FormatInt(rl.getLimit(result.LimitedBy), 10))
				c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
				c.Header("Retry-After", strconv.FormatInt(int64(result.RetryAfter.Seconds()), 10))
			}

			c.JSON(http.StatusTooManyRequests, headers)
			c.Abort()
			return
		}

		c.Next()
	}
}

func CreateEndpointKey(method, path string) string {
	return fmt.Sprintf("%s:%s", method, path)
}

func RateLimitFromConfig(cfg config.RateLimitConfig, logger *zap.Logger) func(ctx context.Context, key string) (int64, error) {
	return func(ctx context.Context, key string) (int64, error) {
		return cfg.GlobalLimit, nil
	}
}

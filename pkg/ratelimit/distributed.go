package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Limiter defines rate limiting behavior
type Limiter interface {
	// Allow checks if a request should be allowed
	Allow(ctx context.Context, key string) (bool, error)
	
	// AllowN checks if N requests should be allowed
	AllowN(ctx context.Context, key string, n int) (bool, error)
	
	// Reset resets the rate limit for a key
	Reset(ctx context.Context, key string) error
	
	// GetRemaining returns remaining quota
	GetRemaining(ctx context.Context, key string) (int64, error)
}

// Config defines rate limiter configuration
type Config struct {
	// Limit is the maximum number of requests allowed
	Limit int64
	
	// Window is the time window for the rate limit
	Window time.Duration
	
	// KeyPrefix is prepended to all Redis keys
	KeyPrefix string
}

// DistributedLimiter implements distributed rate limiting using Redis
type DistributedLimiter struct {
	redis  *redis.Client
	config Config
	logger *zap.Logger
}

// NewDistributedLimiter creates a new distributed rate limiter
func NewDistributedLimiter(redis *redis.Client, config Config, logger *zap.Logger) *DistributedLimiter {
	if config.KeyPrefix == "" {
		config.KeyPrefix = "ratelimit"
	}
	
	return &DistributedLimiter{
		redis:  redis,
		config: config,
		logger: logger,
	}
}

// Allow checks if a request should be allowed
func (l *DistributedLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.AllowN(ctx, key, 1)
}

// AllowN checks if N requests should be allowed using sliding window algorithm
func (l *DistributedLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	redisKey := l.makeKey(key)
	now := time.Now()
	windowStart := now.Add(-l.config.Window)
	
	// Use pipeline for atomic operations
	pipe := l.redis.Pipeline()
	
	// Remove old entries outside the window
	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", windowStart.UnixNano()))
	
	// Count current requests in window
	countCmd := pipe.ZCount(ctx, redisKey, fmt.Sprintf("%d", windowStart.UnixNano()), "+inf")
	
	// Add new request(s)
	for i := 0; i < n; i++ {
		// Use unique timestamp with nanosecond precision to avoid collisions
		timestamp := now.Add(time.Duration(i) * time.Nanosecond).UnixNano()
		pipe.ZAdd(ctx, redisKey, redis.Z{
			Score:  float64(timestamp),
			Member: timestamp,
		})
	}
	
	// Set expiration
	pipe.Expire(ctx, redisKey, l.config.Window*2)
	
	// Execute pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		l.logger.Error("Failed to execute rate limit pipeline",
			zap.Error(err),
			zap.String("key", key))
		return false, fmt.Errorf("rate limit check failed: %w", err)
	}
	
	// Check if limit exceeded
	currentCount := countCmd.Val()
	allowed := currentCount <= l.config.Limit
	
	if !allowed {
		l.logger.Debug("Rate limit exceeded",
			zap.String("key", key),
			zap.Int64("current", currentCount),
			zap.Int64("limit", l.config.Limit))
	}
	
	return allowed, nil
}

// Reset resets the rate limit for a key
func (l *DistributedLimiter) Reset(ctx context.Context, key string) error {
	redisKey := l.makeKey(key)
	
	err := l.redis.Del(ctx, redisKey).Err()
	if err != nil {
		l.logger.Error("Failed to reset rate limit",
			zap.Error(err),
			zap.String("key", key))
		return fmt.Errorf("failed to reset rate limit: %w", err)
	}
	
	return nil
}

// GetRemaining returns remaining quota
func (l *DistributedLimiter) GetRemaining(ctx context.Context, key string) (int64, error) {
	redisKey := l.makeKey(key)
	now := time.Now()
	windowStart := now.Add(-l.config.Window)
	
	// Count current requests
	count, err := l.redis.ZCount(ctx, redisKey,
		fmt.Sprintf("%d", windowStart.UnixNano()),
		"+inf").Result()
	
	if err != nil {
		l.logger.Error("Failed to get remaining quota",
			zap.Error(err),
			zap.String("key", key))
		return 0, fmt.Errorf("failed to get remaining quota: %w", err)
	}
	
	remaining := l.config.Limit - count
	if remaining < 0 {
		remaining = 0
	}
	
	return remaining, nil
}

// makeKey creates a Redis key with prefix
func (l *DistributedLimiter) makeKey(key string) string {
	return fmt.Sprintf("%s:%s", l.config.KeyPrefix, key)
}

// PerUserLimiter creates a rate limiter for per-user limits
func PerUserLimiter(redis *redis.Client, limit int64, window time.Duration, logger *zap.Logger) *DistributedLimiter {
	return NewDistributedLimiter(redis, Config{
		Limit:     limit,
		Window:    window,
		KeyPrefix: "ratelimit:user",
	}, logger)
}

// PerIPLimiter creates a rate limiter for per-IP limits
func PerIPLimiter(redis *redis.Client, limit int64, window time.Duration, logger *zap.Logger) *DistributedLimiter {
	return NewDistributedLimiter(redis, Config{
		Limit:     limit,
		Window:    window,
		KeyPrefix: "ratelimit:ip",
	}, logger)
}

// PerEndpointLimiter creates a rate limiter for per-endpoint limits
func PerEndpointLimiter(redis *redis.Client, limit int64, window time.Duration, logger *zap.Logger) *DistributedLimiter {
	return NewDistributedLimiter(redis, Config{
		Limit:     limit,
		Window:    window,
		KeyPrefix: "ratelimit:endpoint",
	}, logger)
}

// GlobalLimiter creates a global rate limiter
func GlobalLimiter(redis *redis.Client, limit int64, window time.Duration, logger *zap.Logger) *DistributedLimiter {
	return NewDistributedLimiter(redis, Config{
		Limit:     limit,
		Window:    window,
		KeyPrefix: "ratelimit:global",
	}, logger)
}

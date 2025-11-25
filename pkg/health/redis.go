package health

import (
	"context"
	"time"
	
	"github.com/go-redis/redis/v8"
)

// RedisChecker checks Redis connectivity
type RedisChecker struct {
	client  redis.UniversalClient
	timeout time.Duration
}

// NewRedisChecker creates a new Redis health checker
func NewRedisChecker(client redis.UniversalClient, timeout time.Duration) *RedisChecker {
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	
	return &RedisChecker{
		client:  client,
		timeout: timeout,
	}
}

// Check performs the Redis health check
func (c *RedisChecker) Check(ctx context.Context) CheckResult {
	start := time.Now()
	
	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	
	// Ping Redis
	pingResult, err := c.client.Ping(ctx).Result()
	if err != nil {
		return NewUnhealthyResult("redis", err).WithDuration(time.Since(start))
	}
	
	if pingResult != "PONG" {
		return NewUnhealthyResult("redis", nil).
			WithDuration(time.Since(start)).
			WithMetadata("error", "unexpected ping response")
	}
	
	// Get Redis info
	info, err := c.client.Info(ctx, "stats").Result()
	if err != nil {
		// Info failure doesn't make Redis unhealthy, just degraded
		return NewDegradedResult("redis", "connected but unable to get stats").
			WithDuration(time.Since(start))
	}
	
	// Test a simple SET/GET operation
	testKey := "__health_check__"
	testValue := time.Now().Unix()
	
	err = c.client.Set(ctx, testKey, testValue, time.Second*10).Err()
	if err != nil {
		return NewUnhealthyResult("redis", err).WithDuration(time.Since(start))
	}
	
	val, err := c.client.Get(ctx, testKey).Int64()
	if err != nil {
		return NewUnhealthyResult("redis", err).WithDuration(time.Since(start))
	}
	
	if val != testValue {
		return NewUnhealthyResult("redis", nil).
			WithDuration(time.Since(start)).
			WithMetadata("error", "data integrity check failed")
	}
	
	// Clean up test key
	c.client.Del(ctx, testKey)
	
	result := NewHealthyResult("redis", "connected").
		WithDuration(time.Since(start))
	
	// Add connection info if available
	if len(info) > 0 {
		result = result.WithMetadata("info_available", true)
	}
	
	return result
}

// Name returns the checker name
func (c *RedisChecker) Name() string {
	return "redis"
}

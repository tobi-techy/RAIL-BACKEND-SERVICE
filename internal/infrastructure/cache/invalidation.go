package cache

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

type InvalidationStrategy string

const (
	InvalidateImmediate InvalidationStrategy = "immediate"
	InvalidateLazy      InvalidationStrategy = "lazy"
	InvalidateTTL       InvalidationStrategy = "ttl"
)

type CacheInvalidator struct {
	client   RedisClient
	logger   *zap.Logger
	strategy InvalidationStrategy
}

func NewCacheInvalidator(client RedisClient, logger *zap.Logger, strategy InvalidationStrategy) *CacheInvalidator {
	return &CacheInvalidator{
		client:   client,
		logger:   logger,
		strategy: strategy,
	}
}

func (ci *CacheInvalidator) InvalidatePattern(ctx context.Context, pattern string) error {
	keys, err := ci.client.Keys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("failed to get keys for pattern %s: %w", pattern, err)
	}

	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if err := ci.client.Del(ctx, key); err != nil {
			ci.logger.Error("Failed to delete cache key", zap.String("key", key), zap.Error(err))
		}
	}

	return nil
}

func (ci *CacheInvalidator) InvalidateUser(ctx context.Context, userID string) error {
	patterns := []string{
		fmt.Sprintf("user:%s:*", userID),
		fmt.Sprintf("balance:%s:*", userID),
		fmt.Sprintf("wallet:%s:*", userID),
		fmt.Sprintf("portfolio:%s:*", userID),
	}

	for _, pattern := range patterns {
		if err := ci.InvalidatePattern(ctx, pattern); err != nil {
			return err
		}
	}

	return nil
}

func (ci *CacheInvalidator) InvalidateWithTTL(ctx context.Context, key string, ttl time.Duration) error {
	return ci.client.Expire(ctx, key, ttl)
}

func (ci *CacheInvalidator) InvalidateMultiple(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if err := ci.client.Del(ctx, key); err != nil {
			ci.logger.Error("Failed to delete cache key", zap.String("key", key), zap.Error(err))
		}
	}
	return nil
}

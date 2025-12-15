package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

type DistributedCache struct {
	client  *redis.ClusterClient
	logger  *zap.Logger
	prefix  string
	defaultTTL time.Duration
}

type ClusterConfig struct {
	Addrs      []string
	Password   string
	MaxRetries int
	PoolSize   int
}

func NewDistributedCache(cfg *ClusterConfig, logger *zap.Logger) (*DistributedCache, error) {
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:      cfg.Addrs,
		Password:   cfg.Password,
		MaxRetries: cfg.MaxRetries,
		PoolSize:   cfg.PoolSize,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis cluster: %w", err)
	}

	return &DistributedCache{
		client:     client,
		logger:     logger,
		prefix:     "stack:",
		defaultTTL: 1 * time.Hour,
	}, nil
}

func (dc *DistributedCache) Get(ctx context.Context, key string) (string, error) {
	fullKey := dc.prefix + key
	val, err := dc.client.Get(ctx, fullKey).Result()
	if err == redis.Nil {
		return "", nil
	}
	return val, err
}

func (dc *DistributedCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	fullKey := dc.prefix + key
	if ttl == 0 {
		ttl = dc.defaultTTL
	}
	return dc.client.Set(ctx, fullKey, value, ttl).Err()
}

func (dc *DistributedCache) Del(ctx context.Context, key string) error {
	fullKey := dc.prefix + key
	return dc.client.Del(ctx, fullKey).Err()
}

func (dc *DistributedCache) Exists(ctx context.Context, key string) (bool, error) {
	fullKey := dc.prefix + key
	count, err := dc.client.Exists(ctx, fullKey).Result()
	return count > 0, err
}

func (dc *DistributedCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	fullKey := dc.prefix + key
	return dc.client.Expire(ctx, fullKey, ttl).Err()
}

func (dc *DistributedCache) Keys(ctx context.Context, pattern string) ([]string, error) {
	fullPattern := dc.prefix + pattern
	var keys []string
	
	err := dc.client.ForEachMaster(ctx, func(ctx context.Context, client *redis.Client) error {
		iter := client.Scan(ctx, 0, fullPattern, 0).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		return iter.Err()
	})
	
	return keys, err
}

func (dc *DistributedCache) Close() error {
	return dc.client.Close()
}

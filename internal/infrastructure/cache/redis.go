package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/infrastructure/config"
)

// RedisClient defines the interface for Redis operations
type RedisClient interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
	Del(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
	Keys(ctx context.Context, pattern string) ([]string, error)
	Ping(ctx context.Context) error
	Close() error
	Client() *redis.Client
}

// redisClient implements RedisClient using go-redis
type redisClient struct {
	client *redis.Client
	logger *zap.Logger
	config *config.RedisConfig
}

// NewRedisClient creates a new Redis client
func NewRedisClient(cfg *config.RedisConfig, logger *zap.Logger) (RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password, // no password set
		DB:       cfg.DB,       // use default DB
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Connected to Redis successfully", zap.String("host", cfg.Host), zap.Int("port", cfg.Port))

	return &redisClient{
		client: rdb,
		logger: logger,
		config: cfg,
	}, nil
}

// Set sets a key-value pair with an expiration
func (r *redisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	return r.client.Set(ctx, key, data, expiration).Err()
}

// Get retrieves a value by key and unmarshals it into dest
func (r *redisClient) Get(ctx context.Context, key string, dest interface{}) error {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return fmt.Errorf("key '%s' not found: %w", key, err)
	} else if err != nil {
		return fmt.Errorf("failed to get key '%s' from Redis: %w", key, err)
	}
	return json.Unmarshal([]byte(val), dest)
}

// Del deletes a key
func (r *redisClient) Del(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

// Exists checks if a key exists
func (r *redisClient) Exists(ctx context.Context, key string) (bool, error) {
	count, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check existence of key '%s': %w", key, err)
	}
	return count > 0, nil
}

// Incr increments the integer value of a key by one. If the key does not exist, it is set to 0 before performing the operation.
func (r *redisClient) Incr(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, key).Result()
}

// Expire sets a timeout on key. After the timeout has expired, the key will automatically be deleted.
func (r *redisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return r.client.Expire(ctx, key, expiration).Err()
}

// Keys returns all keys matching pattern
func (r *redisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return r.client.Keys(ctx, pattern).Result()
}

// Ping checks the connection to Redis
func (r *redisClient) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close closes the Redis client
func (r *redisClient) Close() error {
	return r.client.Close()
}

// Client returns the underlying Redis client for advanced operations
func (r *redisClient) Client() *redis.Client {
	return r.client
}

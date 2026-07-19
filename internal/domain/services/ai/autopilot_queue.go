package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// AutopilotQueueAction is a pre-approved action queued for the evening review phase.
type AutopilotQueueAction struct {
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args"`
	Reason    string                 `json:"reason"`
	CreatedAt time.Time              `json:"created_at"`
}

// AutopilotQueue stores and retrieves queued autopilot actions per user (Redis-backed).
type AutopilotQueue interface {
	Push(ctx context.Context, userID uuid.UUID, action AutopilotQueueAction) error
	Pop(ctx context.Context, userID uuid.UUID) (*AutopilotQueueAction, error)
	List(ctx context.Context, userID uuid.UUID) ([]AutopilotQueueAction, error)
	Clear(ctx context.Context, userID uuid.UUID) error
	Len(ctx context.Context, userID uuid.UUID) (int, error)
}

const autoQueuePrefix = "autopilot:queue:"

type redisAutopilotQueue struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

func NewRedisAutopilotQueue(redis cache.RedisClient, logger *zap.Logger) AutopilotQueue {
	return &redisAutopilotQueue{redis: redis, logger: logger}
}

func (q *redisAutopilotQueue) queueKey(userID uuid.UUID) string {
	return autoQueuePrefix + userID.String()
}

func (q *redisAutopilotQueue) Push(ctx context.Context, userID uuid.UUID, action AutopilotQueueAction) error {
	action.CreatedAt = time.Now()
	data, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("autopilot queue: marshal action: %w", err)
	}
	pipe := q.redis.Client().Pipeline()
	pipe.RPush(ctx, q.queueKey(userID), data)
	pipe.Expire(ctx, q.queueKey(userID), 24*time.Hour)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("autopilot queue: push: %w", err)
	}
	return nil
}

func (q *redisAutopilotQueue) Pop(ctx context.Context, userID uuid.UUID) (*AutopilotQueueAction, error) {
	data, err := q.redis.Client().LPop(ctx, q.queueKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("autopilot queue: pop: %w", err)
	}
	var act AutopilotQueueAction
	if err := json.Unmarshal([]byte(data), &act); err != nil {
		return nil, fmt.Errorf("autopilot queue: unmarshal action: %w", err)
	}
	return &act, nil
}

func (q *redisAutopilotQueue) List(ctx context.Context, userID uuid.UUID) ([]AutopilotQueueAction, error) {
	raw, err := q.redis.Client().LRange(ctx, q.queueKey(userID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("autopilot queue: list: %w", err)
	}
	actions := make([]AutopilotQueueAction, 0, len(raw))
	for _, item := range raw {
		var act AutopilotQueueAction
		if err := json.Unmarshal([]byte(item), &act); err != nil {
			q.logger.Warn("autopilot queue: skipping corrupt action", zap.Error(err))
			continue
		}
		actions = append(actions, act)
	}
	return actions, nil
}

func (q *redisAutopilotQueue) Clear(ctx context.Context, userID uuid.UUID) error {
	return q.redis.Del(ctx, q.queueKey(userID))
}

func (q *redisAutopilotQueue) Len(ctx context.Context, userID uuid.UUID) (int, error) {
	n, err := q.redis.Client().LLen(ctx, q.queueKey(userID)).Result()
	if err != nil {
		return 0, fmt.Errorf("autopilot queue: len: %w", err)
	}
	return int(n), nil
}

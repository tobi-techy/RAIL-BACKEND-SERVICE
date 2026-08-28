package goals

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

const savingsGoalKeyPrefix = "savings_goal:"

// RedisSavingsGoalStore stores savings goals in Redis.
type RedisSavingsGoalStore struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

// NewRedisSavingsGoalStore creates a Redis-backed savings goal store.
func NewRedisSavingsGoalStore(redis cache.RedisClient, logger *zap.Logger) ai.SavingsGoalStore {
	return &RedisSavingsGoalStore{redis: redis, logger: logger}
}

func (r *RedisSavingsGoalStore) Set(ctx context.Context, userID uuid.UUID, goal *ai.SavingsGoal) error {
	return r.redis.Set(ctx, savingsGoalKeyPrefix+userID.String(), goal, 365*24*time.Hour)
}

func (r *RedisSavingsGoalStore) Get(ctx context.Context, userID uuid.UUID) (*ai.SavingsGoal, error) {
	var goal ai.SavingsGoal
	if err := r.redis.Get(ctx, savingsGoalKeyPrefix+userID.String(), &goal); err != nil {
		return nil, err
	}
	return &goal, nil
}

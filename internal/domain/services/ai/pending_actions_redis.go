package ai

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

const pendingActionKeyPrefix = "pending_action:"

// RedisPendingActions stores pending actions in Redis with automatic TTL expiry.
type RedisPendingActions struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

// NewRedisPendingActions creates a Redis-backed pending action store.
func NewRedisPendingActions(redis cache.RedisClient, logger *zap.Logger) PendingActionStore {
	return &RedisPendingActions{redis: redis, logger: logger}
}

func (r *RedisPendingActions) Set(ctx context.Context, convID uuid.UUID, action *entities.PendingAction) error {
	return r.redis.Set(ctx, pendingActionKeyPrefix+convID.String(), action, pendingActionTTL)
}

func (r *RedisPendingActions) Get(ctx context.Context, convID uuid.UUID) *entities.PendingAction {
	var action entities.PendingAction
	if err := r.redis.Get(ctx, pendingActionKeyPrefix+convID.String(), &action); err != nil {
		return nil
	}
	if action.IsExpired() {
		r.Delete(ctx, convID)
		return nil
	}
	return &action
}

func (r *RedisPendingActions) Delete(ctx context.Context, convID uuid.UUID) {
	_ = r.redis.Del(ctx, pendingActionKeyPrefix+convID.String())
}

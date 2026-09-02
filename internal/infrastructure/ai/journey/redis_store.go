package journey

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// RedisJourneyStore stores journey state in Redis with a long TTL so a user
// who disappears mid-onboarding picks up exactly where they left off.
type RedisJourneyStore struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

// NewRedisJourneyStore creates a Redis-backed journey store.
func NewRedisJourneyStore(redis cache.RedisClient, logger *zap.Logger) ai.JourneyStore {
	return &RedisJourneyStore{redis: redis, logger: logger}
}

const journeyKeyPrefix = "miriam_journey:"
const journeyStateTTL = 180 * 24 * time.Hour

func (r *RedisJourneyStore) Get(ctx context.Context, userID uuid.UUID) (*ai.JourneyState, error) {
	var state ai.JourneyState
	err := r.redis.Get(ctx, journeyKeyPrefix+userID.String(), &state)
	if err != nil {
		// Return empty state for missing key (new user), propagate real errors
		if errStr := err.Error(); strings.Contains(errStr, "not found") || strings.Contains(errStr, "redis: nil") {
			return &ai.JourneyState{UserID: userID.String()}, nil
		}
		return nil, err
	}
	state.UserID = userID.String()
	return &state, nil
}

func (r *RedisJourneyStore) Save(ctx context.Context, state *ai.JourneyState) error {
	state.UpdatedAt = time.Now().UTC()
	return r.redis.Set(ctx, journeyKeyPrefix+state.UserID, state, journeyStateTTL)
}

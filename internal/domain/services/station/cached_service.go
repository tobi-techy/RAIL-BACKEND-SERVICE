package station

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
)

const (
	balancesTTL = 10 * time.Second
	stationTTL  = 10 * time.Second
)

func balancesKey(userID uuid.UUID) string {
	return fmt.Sprintf("station:balances:%s", userID)
}

// CachedService wraps Service with Redis caching for GetUserBalances.
type CachedService struct {
	*Service
	redis cache.RedisClient
}

func NewCachedService(svc *Service, redis cache.RedisClient) *CachedService {
	return &CachedService{Service: svc, redis: redis}
}

func (c *CachedService) GetUserBalances(ctx context.Context, userID uuid.UUID) (*Balances, error) {
	key := balancesKey(userID)

	var cached Balances
	if err := c.redis.Get(ctx, key, &cached); err == nil {
		return &cached, nil
	}

	balances, err := c.Service.GetUserBalances(ctx, userID)
	if err != nil {
		return nil, err
	}

	_ = c.redis.Set(ctx, key, balances, balancesTTL)
	return balances, nil
}

// InvalidateBalances removes cached balances for a user (call after deposit/withdrawal).
func (c *CachedService) InvalidateBalances(ctx context.Context, userID uuid.UUID) {
	_ = c.redis.Del(ctx, balancesKey(userID))
}

package analytics

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

const (
	ttlRealtime   = 60 * time.Second
	ttlHistorical = 5 * time.Minute
	keyPrefix     = "analytics:"
)

type Service struct {
	repo   *repositories.AnalyticsRepository
	cache  cache.RedisClient
	logger *zap.Logger
}

func NewService(repo *repositories.AnalyticsRepository, cache cache.RedisClient, logger *zap.Logger) *Service {
	return &Service{repo: repo, cache: cache, logger: logger}
}

func (s *Service) GetOverview(ctx context.Context) (*repositories.OverviewData, error) {
	var result repositories.OverviewData
	if s.fromCache(ctx, "overview", &result) {
		return &result, nil
	}
	data, err := s.repo.GetOverview(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "overview", data, ttlRealtime)
	return data, nil
}

func (s *Service) GetUsers(ctx context.Context, limit, offset int) (*repositories.UsersData, error) {
	var result repositories.UsersData
	key := "users"
	if s.fromCache(ctx, key, &result) {
		return &result, nil
	}
	data, err := s.repo.GetUsers(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, key, data, ttlRealtime)
	return data, nil
}

func (s *Service) GetWaitlist(ctx context.Context) (*repositories.WaitlistData, error) {
	var result repositories.WaitlistData
	if s.fromCache(ctx, "waitlist", &result) {
		return &result, nil
	}
	data, err := s.repo.GetWaitlist(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "waitlist", data, ttlHistorical)
	return data, nil
}

func (s *Service) GetMiriam(ctx context.Context) (*repositories.MiriamData, error) {
	var result repositories.MiriamData
	if s.fromCache(ctx, "miriam", &result) {
		return &result, nil
	}
	data, err := s.repo.GetMiriam(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "miriam", data, ttlRealtime)
	return data, nil
}

func (s *Service) GetMoneyMovement(ctx context.Context) (*repositories.MoneyMovementData, error) {
	var result repositories.MoneyMovementData
	if s.fromCache(ctx, "money_movement", &result) {
		return &result, nil
	}
	data, err := s.repo.GetMoneyMovement(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "money_movement", data, ttlHistorical)
	return data, nil
}

func (s *Service) GetRetention(ctx context.Context) (*repositories.RetentionData, error) {
	var result repositories.RetentionData
	if s.fromCache(ctx, "retention", &result) {
		return &result, nil
	}
	data, err := s.repo.GetRetention(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "retention", data, ttlHistorical)
	return data, nil
}

func (s *Service) GetTrust(ctx context.Context) (*repositories.TrustData, error) {
	var result repositories.TrustData
	if s.fromCache(ctx, "trust", &result) {
		return &result, nil
	}
	data, err := s.repo.GetTrust(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "trust", data, ttlHistorical)
	return data, nil
}

func (s *Service) GetChains(ctx context.Context) (*repositories.ChainsData, error) {
	var result repositories.ChainsData
	if s.fromCache(ctx, "chains", &result) {
		return &result, nil
	}
	data, err := s.repo.GetChains(ctx)
	if err != nil {
		return nil, err
	}
	s.toCache(ctx, "chains", data, ttlHistorical)
	return data, nil
}

func (s *Service) fromCache(ctx context.Context, key string, dest interface{}) bool {
	if s.cache == nil {
		return false
	}
	err := s.cache.Get(ctx, keyPrefix+key, dest)
	return err == nil
}

func (s *Service) toCache(ctx context.Context, key string, value interface{}, ttl time.Duration) {
	if s.cache == nil {
		return
	}
	if err := s.cache.Set(ctx, keyPrefix+key, value, ttl); err != nil {
		s.logger.Warn("analytics cache set failed", zap.String("key", key), zap.Error(err))
	}
}

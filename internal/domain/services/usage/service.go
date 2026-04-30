package usage

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Repository defines the persistence operations the usage service needs.
type Repository interface {
	Upsert(ctx context.Context, userID uuid.UUID, messages int, voiceSeconds int, cost decimal.Decimal, model string) error
	GetByUserPeriod(ctx context.Context, userID uuid.UUID, period time.Time) (*entities.AIUsage, error)
}

// Service tracks AI usage and enforces cost ceilings.
type Service struct {
	repo   Repository
	logger *zap.Logger
}

// NewService creates a new usage service.
func NewService(repo Repository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// TrackInteraction records a chat interaction's usage and cost.
func (s *Service) TrackInteraction(ctx context.Context, userID uuid.UUID, model string, tokens int) error {
	cost := entities.EstimateCost(model, tokens)
	return s.repo.Upsert(ctx, userID, 1, 0, cost, model)
}

// TrackVoice records voice usage seconds.
func (s *Service) TrackVoice(ctx context.Context, userID uuid.UUID, seconds int) error {
	// Whisper cost: ~$0.006/min
	cost := decimal.NewFromFloat(float64(seconds) * 0.0001)
	return s.repo.Upsert(ctx, userID, 0, seconds, cost, "whisper")
}

// GetCurrentUsage returns usage stats for the current billing period.
func (s *Service) GetCurrentUsage(ctx context.Context, userID uuid.UUID) (*entities.AIUsage, error) {
	return s.repo.GetByUserPeriod(ctx, userID, time.Now())
}

// IsOverCostCeiling returns true if the user has exceeded the monthly or daily cost ceiling.
func (s *Service) IsOverCostCeiling(ctx context.Context, userID uuid.UUID) (bool, error) {
	u, err := s.repo.GetByUserPeriod(ctx, userID, time.Now())
	if err != nil {
		return false, err
	}
	if u.EstimatedCost.GreaterThanOrEqual(entities.CostCeilingUSD) {
		return true, nil
	}
	// Estimate daily spend: monthly cost / days elapsed so far
	// This prevents a user from burning the entire monthly budget in one day.
	now := time.Now().UTC()
	dayOfMonth := now.Day()
	if dayOfMonth < 1 {
		dayOfMonth = 1
	}
	dailyAvg := u.EstimatedCost.Div(decimal.NewFromInt(int64(dayOfMonth)))
	return dailyAvg.GreaterThanOrEqual(entities.DailyCostCeilingUSD), nil
}

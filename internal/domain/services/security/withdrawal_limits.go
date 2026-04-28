package security

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

type WithdrawalLimitsService struct {
	repo   *repositories.SecurityFeaturesRepository
	logger *zap.Logger
}

func NewWithdrawalLimitsService(repo *repositories.SecurityFeaturesRepository, logger *zap.Logger) *WithdrawalLimitsService {
	return &WithdrawalLimitsService{repo: repo, logger: logger}
}

type TierLimits struct {
	Tier     entities.WithdrawalTier `json:"tier"`
	DayMax   decimal.Decimal         `json:"day_max"`
	WeekMax  decimal.Decimal         `json:"week_max"`
	DayUsed  decimal.Decimal         `json:"day_used"`
	WeekUsed decimal.Decimal         `json:"week_used"`
}

var tierConfig = map[entities.WithdrawalTier]struct {
	dayMax  int64
	weekMax int64
}{
	entities.WithdrawalTier1: {500, 2000},
	entities.WithdrawalTier2: {2000, 10000},
	entities.WithdrawalTier3: {10000, 50000},
}

func DetermineTier(accountAge time.Duration, kycLevel string) entities.WithdrawalTier {
	days := accountAge.Hours() / 24
	if days > 90 && kycLevel == "full" {
		return entities.WithdrawalTier3
	}
	if days > 30 {
		return entities.WithdrawalTier2
	}
	return entities.WithdrawalTier1
}

func (s *WithdrawalLimitsService) CheckWithdrawalLimit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, accountAge time.Duration, kycLevel string) (*TierLimits, error) {
	tier := DetermineTier(accountAge, kycLevel)
	cfg := tierConfig[tier]
	dayMax := decimal.NewFromInt(cfg.dayMax)
	weekMax := decimal.NewFromInt(cfg.weekMax)

	now := time.Now()
	dayKey := now.Format("2006-01-02")
	_, week := now.ISOWeek()
	weekKey := fmt.Sprintf("%d-W%02d", now.Year(), week)

	dayUsed, _ := s.repo.GetPeriodUsage(ctx, userID, "day", dayKey)
	weekUsed, _ := s.repo.GetPeriodUsage(ctx, userID, "week", weekKey)

	result := &TierLimits{
		Tier:     tier,
		DayMax:   dayMax,
		WeekMax:  weekMax,
		DayUsed:  dayUsed,
		WeekUsed: weekUsed,
	}

	if dayUsed.Add(amount).GreaterThan(dayMax) {
		return result, fmt.Errorf("daily withdrawal limit exceeded (tier %d: $%s/day)", tier, dayMax.String())
	}
	if weekUsed.Add(amount).GreaterThan(weekMax) {
		return result, fmt.Errorf("weekly withdrawal limit exceeded (tier %d: $%s/week)", tier, weekMax.String())
	}

	return result, nil
}

func (s *WithdrawalLimitsService) RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	now := time.Now()
	dayKey := now.Format("2006-01-02")
	_, week := now.ISOWeek()
	weekKey := fmt.Sprintf("%d-W%02d", now.Year(), week)

	dayUsage := &entities.WithdrawalLimitUsage{
		ID: uuid.New(), UserID: userID, Amount: amount, PeriodType: "day", PeriodKey: dayKey, CreatedAt: now,
	}
	weekUsage := &entities.WithdrawalLimitUsage{
		ID: uuid.New(), UserID: userID, Amount: amount, PeriodType: "week", PeriodKey: weekKey, CreatedAt: now,
	}

	if err := s.repo.RecordWithdrawalUsage(ctx, dayUsage); err != nil {
		return err
	}
	return s.repo.RecordWithdrawalUsage(ctx, weekUsage)
}

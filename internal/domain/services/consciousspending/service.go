package consciousspending

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidIncome    = errors.New("take-home income must be positive")
	ErrInvalidPlanTotal = errors.New("the four buckets must equal take-home income")
)

type Service struct {
	repo  repositories.ConsciousSpendingPlanRepository
	clock func() time.Time
}

func NewService(repo repositories.ConsciousSpendingPlanRepository) *Service {
	return &Service{repo: repo, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetClock(clock func() time.Time) {
	if clock != nil {
		s.clock = clock
	}
}

type PlanInput struct {
	TakeHomeIncome    decimal.Decimal
	Currency          string
	FixedCosts        decimal.Decimal
	Investments       decimal.Decimal
	Savings           decimal.Decimal
	GuiltFreeSpending decimal.Decimal
	CheckInCadence    string
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return s.repo.GetByUserID(ctx, userID)
}

func (s *Service) Commit(ctx context.Context, userID uuid.UUID, in PlanInput) (*entities.ConsciousSpendingPlan, error) {
	return s.save(ctx, userID, in, entities.ConsciousSpendingPlanStatusCommitted)
}

func (s *Service) Pause(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	plan, err := s.repo.GetByUserID(ctx, userID)
	if err != nil || plan == nil {
		return plan, err
	}
	plan.Status = entities.ConsciousSpendingPlanStatusPaused
	if err := s.repo.Upsert(ctx, plan); err != nil {
		return nil, err
	}
	return s.repo.GetByUserID(ctx, userID)
}

func (s *Service) ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	return s.repo.ListCommittedCheckIns(ctx)
}

func (s *Service) save(ctx context.Context, userID uuid.UUID, in PlanInput, status string) (*entities.ConsciousSpendingPlan, error) {
	if userID == uuid.Nil {
		return nil, errors.New("user id is required")
	}
	if err := ValidateInput(in); err != nil {
		return nil, err
	}
	now := s.clock()
	plan := &entities.ConsciousSpendingPlan{
		UserID:               userID,
		TakeHomeIncome:       in.TakeHomeIncome,
		Currency:             strings.ToUpper(strings.TrimSpace(in.Currency)),
		FixedCosts:           in.FixedCosts,
		Investments:          in.Investments,
		Savings:              in.Savings,
		GuiltFreeSpending:    in.GuiltFreeSpending,
		FixedCostsPct:        percentage(in.FixedCosts, in.TakeHomeIncome),
		InvestmentsPct:       percentage(in.Investments, in.TakeHomeIncome),
		SavingsPct:           percentage(in.Savings, in.TakeHomeIncome),
		GuiltFreeSpendingPct: percentage(in.GuiltFreeSpending, in.TakeHomeIncome),
		Status:               status,
		CheckInCadence:       normalizeCadence(in.CheckInCadence),
	}
	if status == entities.ConsciousSpendingPlanStatusCommitted {
		plan.CommittedAt = &now
	}
	if err := s.repo.Upsert(ctx, plan); err != nil {
		return nil, fmt.Errorf("save conscious spending plan: %w", err)
	}
	return s.repo.GetByUserID(ctx, userID)
}

func ValidateInput(in PlanInput) error {
	if !in.TakeHomeIncome.IsPositive() {
		return ErrInvalidIncome
	}
	if strings.TrimSpace(in.Currency) == "" {
		return errors.New("currency is required")
	}
	for name, amount := range map[string]decimal.Decimal{
		"fixed costs": in.FixedCosts, "investments": in.Investments,
		"savings": in.Savings, "guilt-free spending": in.GuiltFreeSpending,
	} {
		if amount.IsNegative() {
			return fmt.Errorf("%s must be non-negative", name)
		}
	}
	total := in.FixedCosts.Add(in.Investments).Add(in.Savings).Add(in.GuiltFreeSpending)
	if total.Sub(in.TakeHomeIncome).Abs().GreaterThan(decimal.NewFromInt(1).Shift(-2)) {
		return ErrInvalidPlanTotal
	}
	return nil
}

func percentage(amount, income decimal.Decimal) decimal.Decimal {
	if !income.IsPositive() {
		return decimal.Zero
	}
	return amount.Div(income).Mul(decimal.NewFromInt(100)).Round(4)
}

func normalizeCadence(cadence string) string {
	switch strings.ToLower(strings.TrimSpace(cadence)) {
	case entities.CheckInCadenceBiweekly:
		return entities.CheckInCadenceBiweekly
	case entities.CheckInCadenceMonthly:
		return entities.CheckInCadenceMonthly
	default:
		return entities.CheckInCadenceWeekly
	}
}

package consciousspending

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidIncome       = errors.New("gross or take-home income must be provided")
	ErrInvalidPlanTotal    = errors.New("the four buckets plus buffer must equal take-home income")
	ErrPlanItemsRequired   = errors.New("plan items are required for committed plans")
	ErrNegativeAmount      = errors.New("amounts must be non-negative")
	ErrInvalidCadence      = errors.New("invalid check-in cadence")
	ErrInvalidBufferRate   = errors.New("buffer rate must be between 0 and 1")
	ErrMissingBaseCurrency = errors.New("base currency is required")
	ErrPlanNotFound        = errors.New("conscious spending plan not found")
)

type Service struct {
	repo           repositories.ConsciousSpendingPlanRepository
	itemNormalizer func(ctx context.Context, userID uuid.UUID, items []entities.ConsciousSpendingPlanItem) ([]entities.ConsciousSpendingPlanItem, error)
	clock          func() time.Time
}

func NewService(repo repositories.ConsciousSpendingPlanRepository) *Service {
	return &Service{repo: repo, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetItemNormalizer(normalizer func(ctx context.Context, userID uuid.UUID, items []entities.ConsciousSpendingPlanItem) ([]entities.ConsciousSpendingPlanItem, error)) {
	s.itemNormalizer = normalizer
}

func (s *Service) SetClock(clock func() time.Time) {
	if clock != nil {
		s.clock = clock
	}
}

func (s *Service) Get(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return s.repo.GetActiveVersion(ctx, userID)
}

func (s *Service) Commit(ctx context.Context, userID uuid.UUID, in repositories.PlanHeaderInput) (*entities.ConsciousSpendingPlan, error) {
	if userID == uuid.Nil {
		return nil, errors.New("user id is required")
	}
	if err := ValidateHeaderInput(in); err != nil {
		return nil, err
	}
	if in.Status == entities.ConsciousSpendingPlanStatusCommitted && len(in.Items) == 0 {
		return nil, ErrPlanItemsRequired
	}
	now := s.clock()
	in.CommittedAt = &now
	in.BaseCurrency = strings.ToUpper(strings.TrimSpace(in.BaseCurrency))
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	in.IncomeSource = strings.TrimSpace(in.IncomeSource)
	in.IncomeConfidence = normalizeConfidence(in.IncomeConfidence)
	in.IncomeCadence = normalizeCadence(in.IncomeCadence)
	in.IncomeBasis = strings.TrimSpace(in.IncomeBasis)
	in.CheckInCadence = normalizeCheckInCadence(in.CheckInCadence)
	in.Status = entities.ConsciousSpendingPlanStatusCommitted
	in.FixedCostsPct = percentage(in.FixedCosts, in.TakeHomeIncome)
	in.InvestmentsPct = percentage(in.PostTaxInvestments, in.TakeHomeIncome)
	in.SavingsPct = percentage(in.Savings, in.TakeHomeIncome)
	in.GuiltFreeSpendingPct = percentage(in.GuiltFreeSpending, in.TakeHomeIncome)
	if s.itemNormalizer != nil {
		normalized, err := s.itemNormalizer(ctx, userID, in.Items)
		if err != nil {
			return nil, fmt.Errorf("normalize plan items: %w", err)
		}
		in.Items = normalized
	}
	sortItems(in.Items)
	if err := reconcileHeaderItems(&in); err != nil {
		return nil, err
	}
	if in.MiscBufferRate.IsPositive() {
		in.MiscBufferAmount = in.FixedCostsSubtotal.Mul(in.MiscBufferRate)
	}
	_, err := s.repo.Commit(ctx, userID, in)
	if err != nil {
		return nil, fmt.Errorf("commit conscious spending plan: %w", err)
	}
	return s.repo.GetActiveVersion(ctx, userID)
}


func (s *Service) Pause(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error) {
	if _, err := s.loadWritable(ctx, userID, version); err != nil {
		return nil, err
	}
	if _, err := s.repo.Pause(ctx, userID, version); err != nil {
		return nil, fmt.Errorf("pause conscious spending plan: %w", err)
	}
	return s.repo.GetActiveVersion(ctx, userID)
}

func (s *Service) Supersede(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error) {
	if version <= 0 {
		return nil, errors.New("version must be positive")
	}
	_, err := s.repo.Supersede(ctx, userID, version)
	if err != nil {
		return nil, fmt.Errorf("supersede conscious spending plan: %w", err)
	}
	return s.repo.GetActiveVersion(ctx, userID)
}

func (s *Service) ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	return s.repo.ListCommittedCheckIns(ctx)
}

func (s *Service) loadWritable(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error) {
	plan, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Version != version || plan.Status != entities.ConsciousSpendingPlanStatusCommitted {
		return nil, ErrPlanNotFound
	}
	return plan, nil
}

func ValidateHeaderInput(in repositories.PlanHeaderInput) error {
	if strings.TrimSpace(in.BaseCurrency) == "" {
		return ErrMissingBaseCurrency
	}
	if in.GrossMonthlyIncome.IsNegative() || in.TakeHomeIncome.IsNegative() {
		return ErrInvalidIncome
	}
	if !in.TakeHomeIncome.IsPositive() && !in.GrossMonthlyIncome.IsPositive() {
		return ErrInvalidIncome
	}
	for name, amount := range map[string]decimal.Decimal{
		"payroll deductions": in.PayrollDeductions, "pre-tax investments": in.PreTaxInvestments,
		"fixed costs subtotal": in.FixedCostsSubtotal, "fixed costs": in.FixedCosts,
		"post-tax investments": in.PostTaxInvestments, "savings": in.Savings,
		"guilt-free spending": in.GuiltFreeSpending,
	} {
		if amount.IsNegative() {
			return fmt.Errorf("%w: %s", ErrNegativeAmount, name)
		}
	}
	if in.MiscBufferRate.IsNegative() || in.MiscBufferRate.GreaterThan(decimal.NewFromInt(1)) {
		return ErrInvalidBufferRate
	}
	total := in.FixedCosts.Add(in.MiscBufferAmount).Add(in.PostTaxInvestments).Add(in.Savings).Add(in.GuiltFreeSpending)
	if total.Sub(in.TakeHomeIncome).Abs().GreaterThan(decimal.NewFromInt(1).Shift(-2)) {
		return ErrInvalidPlanTotal
	}
	if err := validateCadence(in.CheckInCadence); err != nil {
		return err
	}
	return nil
}

func validateCadence(cadence string) error {
	switch strings.ToLower(strings.TrimSpace(cadence)) {
	case "", entities.CheckInCadenceWeekly, entities.CheckInCadenceBiweekly, entities.CheckInCadenceMonthly:
		return nil
	default:
		return ErrInvalidCadence
	}
}

func normalizeCadence(cadence string) string {
	switch strings.ToLower(strings.TrimSpace(cadence)) {
	case entities.CheckInCadenceBiweekly, entities.CheckInCadenceMonthly:
		return strings.ToLower(strings.TrimSpace(cadence))
	default:
		return entities.CheckInCadenceWeekly
	}
}

func normalizeCheckInCadence(cadence string) string {
	return normalizeCadence(cadence)
}

func normalizeConfidence(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "medium", "high":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "low"
	}
}

func sortItems(items []entities.ConsciousSpendingPlanItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Bucket != items[j].Bucket {
			return items[i].Bucket < items[j].Bucket
		}
		if items[i].DisplayOrder != items[j].DisplayOrder {
			return items[i].DisplayOrder < items[j].DisplayOrder
		}
		return items[i].Name < items[j].Name
	})
}

func reconcileHeaderItems(in *repositories.PlanHeaderInput) error {
	var fixedAmount, investmentAmount, savingsAmount decimal.Decimal
	for _, item := range in.Items {
		switch item.Bucket {
		case entities.CSPItemBucketFixedCost:
			fixedAmount = fixedAmount.Add(item.Amount)
		case entities.CSPItemBucketInvestment:
			investmentAmount = investmentAmount.Add(item.Amount)
		case entities.CSPItemBucketSavings:
			savingsAmount = savingsAmount.Add(item.Amount)
		default:
			return fmt.Errorf("unknown bucket %q", item.Bucket)
		}
	}
	in.FixedCosts = fixedAmount
	in.PostTaxInvestments = investmentAmount
	in.Savings = savingsAmount
	in.FixedCostsSubtotal = fixedAmount.Add(in.MiscBufferAmount)
	return nil
}

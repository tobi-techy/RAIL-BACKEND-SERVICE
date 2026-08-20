package goals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// FinancialProfileProvider is the minimal subset of the financial-profile
// service that BabyStepsSeed needs. Implemented by the existing profile
// service; we keep the surface tight so the seed has no opinion on storage.
type FinancialProfileProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
}

// ObligationDebtLister lists active debt obligations for the snowball seeds.
type ObligationDebtLister interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

// BabyStepsSeed materializes the 7-step ladder into user_goals for a new user.
// Idempotent: returns (0, nil) when any goal already exists for the user.
type BabyStepsSeed struct {
	goals       *Service
	repo        repositories.UserGoalRepository // for direct HasAnyGoal check + insert path
	profile     FinancialProfileProvider
	obligations ObligationDebtLister
	logger      *zap.Logger
}

// NewBabyStepsSeed constructs the seeder.
func NewBabyStepsSeed(
	goals *Service,
	repo repositories.UserGoalRepository,
	profile FinancialProfileProvider,
	obligations ObligationDebtLister,
	logger *zap.Logger,
) *BabyStepsSeed {
	return &BabyStepsSeed{
		goals:       goals,
		repo:        repo,
		profile:     profile,
		obligations: obligations,
		logger:      logger,
	}
}

// Seed creates the default goal ladder for the user. Safe to call repeatedly
// (no-ops when any goal already exists). Returns the number of goals created.
func (s *BabyStepsSeed) Seed(ctx context.Context, userID uuid.UUID) (int, error) {
	if userID == uuid.Nil {
		return 0, errors.New("seed baby steps: user id is required")
	}
	has, err := s.repo.HasAnyGoal(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("seed baby steps: %w", err)
	}
	if has {
		return 0, nil
	}

	// Profile may be nil for a brand-new user — fall back to defaults.
	var profile *entities.FinancialProfile
	if s.profile != nil {
		p, perr := s.profile.GetByUserID(ctx, userID)
		if perr != nil {
			if s.logger != nil {
				s.logger.Debug("seed baby steps: profile fetch failed, using defaults",
					zap.String("user_id", userID.String()), zap.Error(perr))
			}
		} else {
			profile = p
		}
	}

	debts, _ := s.fetchDebts(ctx, userID)
	created := 0

	// Step 1 — Starter Emergency Fund.
	starterTarget := decimal.NewFromInt(1000)
	if profile != nil && profile.MonthlyFixedCosts.IsPositive() {
		// Use one month of fixed costs as a PPP-scaled starter.
		starterTarget = profile.MonthlyFixedCosts
		if starterTarget.GreaterThan(decimal.NewFromInt(1000)) {
			// Cap so the starter stays achievable in 1-3 months.
			starterTarget = decimal.NewFromInt(1000)
		}
	}
	if err := s.createStepGoal(ctx, userID, 1, entities.GoalCategoryStarterEmergency,
		"Starter emergency fund", starterTarget, nil); err == nil {
		created++
	}

	// Step 2 — Debt snowball. One goal per active debt (smallest first).
	for i, d := range debts {
		if !d.Amount.IsPositive() {
			continue
		}
		name := fmt.Sprintf("Pay off %s", d.Name)
		if i == 0 {
			name = fmt.Sprintf("Snowball: %s (smallest first)", d.Name)
		}
		if err := s.createStepGoal(ctx, userID, 2, entities.GoalCategoryDebtPayoff,
			name, d.Amount, nil); err == nil {
			created++
		}
	}

	// Step 3 — Full Emergency Fund (3 × monthly expenses).
	fullEmergency := decimal.NewFromInt(3000)
	if profile != nil && profile.MonthlyFixedCosts.IsPositive() {
		fullEmergency = profile.MonthlyFixedCosts.Mul(decimal.NewFromInt(3))
	}
	if err := s.createStepGoal(ctx, userID, 3, entities.GoalCategoryFullEmergency,
		"Full emergency fund (3 months)", fullEmergency, nil); err == nil {
		created++
	}

	// Step 4 — Retirement (15% of monthly income × 12 months of cushion, then
	// 15% annual contribution as a target). Keep the target simple — exact
	// retirement math is the chat path's job, not the seed's.
	retirementTarget := decimal.NewFromInt(5000)
	if profile != nil && profile.MonthlyIncome.IsPositive() {
		// One year of 15% contributions as a starter.
		retirementTarget = profile.MonthlyIncome.Mul(decimal.NewFromFloat(0.15)).Mul(decimal.NewFromInt(12))
	}
	if err := s.createStepGoal(ctx, userID, 4, entities.GoalCategoryRetirement,
		"Retirement (15% of income)", retirementTarget, nil); err == nil {
		created++
	}

	// Step 5 — Kids' college. Only seed when the profile mentions dependents.
	if profile != nil && profile.UserType != "" && hasDependents(profile) {
		collegeTarget := decimal.NewFromInt(10000)
		if err := s.createStepGoal(ctx, userID, 5, entities.GoalCategoryCollege,
			"College fund", collegeTarget, nil); err == nil {
			created++
		}
	}

	// Step 6 — Mortgage payoff. Seed only when a mortgage-like obligation
	// exists. Detection is rough; users with mortgages will get one anyway.
	if hasMortgageObligation(debts) {
		mortgageTarget := decimal.NewFromInt(50000)
		if err := s.createStepGoal(ctx, userID, 6, entities.GoalCategoryMortgage,
			"Mortgage payoff", mortgageTarget, nil); err == nil {
			created++
		}
	}

	// Step 7 — Wealth building (open-ended). The target is intentionally
	// generous so the chat path can refine it with the user.
	if err := s.createStepGoal(ctx, userID, 7, entities.GoalCategoryWealth,
		"Wealth building", decimal.NewFromInt(50000), nil); err == nil {
		created++
	}

	return created, nil
}

// createStepGoal inserts a single goal tagged to a Baby Step. Failures are
// logged but do not abort the seed — partial seeding is better than no seed.
func (s *BabyStepsSeed) createStepGoal(ctx context.Context, userID uuid.UUID, step int, category, name string, target decimal.Decimal, deadline *time.Time) error {
	stepVal := step
	in := CreateInput{
		Name:         name,
		TargetAmount: target,
		Deadline:     deadline,
		BabyStep:     &stepVal,
		Category:     category,
		Source:       entities.GoalSourceMiriamOnboard,
	}
	_, err := s.goals.Create(ctx, userID, in)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("baby steps seed: create goal failed",
				zap.String("user_id", userID.String()),
				zap.Int("step", step),
				zap.String("category", category),
				zap.Error(err))
		}
		return err
	}
	return nil
}

// fetchDebts returns the user's active debt obligations sorted by amount
// ascending (snowball order). Errors are swallowed — the seed can still
// proceed without the snowball branch.
func (s *BabyStepsSeed) fetchDebts(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error) {
	if s.obligations == nil {
		return nil, nil
	}
	list, err := s.obligations.ListActive(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Filter to debt-only after the active list is fetched.
	debtsOnly := make([]entities.FinancialObligation, 0, len(list))
	for _, ob := range list {
		if ob.Type == entities.ObligationTypeDebt {
			debtsOnly = append(debtsOnly, ob)
		}
	}
	list = debtsOnly
	// Bubble sort smallest-first (mirrors orchestrator_baby_steps.go).
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].Amount.LessThan(list[i].Amount) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	return list, nil
}

// hasDependents is a rough heuristic for Step 5 seeding. The financial profile
// doesn't carry a dependents field today, so we treat "family_support" +
// non-empty family support country as a proxy. Users without that signal don't
// get a Step 5 goal.
func hasDependents(p *entities.FinancialProfile) bool {
	if p == nil {
		return false
	}
	if p.FamilySupportCountry != "" {
		return true
	}
	if p.UserType == "family" || p.UserType == "parent" {
		return true
	}
	return false
}

// hasMortgageObligation returns true when the user's debt list contains a
// mortgage-shaped entry. The detection is name-only — the obligation model
// doesn't have a structured mortgage flag yet.
func hasMortgageObligation(debts []entities.FinancialObligation) bool {
	for _, d := range debts {
		if d.Type == entities.ObligationTypeRent {
			continue
		}
		name := d.Name
		if d.Counterparty != nil {
			name += " " + *d.Counterparty
		}
		lower := lowerFirst(name)
		if containsAny(lower, []string{"mortgage", "home loan", "house loan"}) {
			return true
		}
	}
	return false
}

// containsAny + lowerFirst are tiny local helpers to avoid pulling in
// strings/unicode just for two checks.
func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}

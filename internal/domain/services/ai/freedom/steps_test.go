package freedom

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

func TestClassifyFreedomStep(t *testing.T) {
	tests := []struct {
		name               string
		state              *entities.MiriamMoneyState
		spendBalance       decimal.Decimal
		stashBalance       decimal.Decimal
		debts              []entities.FinancialObligation
		profile            *entities.FinancialProfile
		hasInvestmentActivity bool
		portfolioValue     decimal.Decimal
		expectedStep       int
	}{
		{
			name:         "brand new user — no data",
			state:        nil,
			spendBalance: decimal.Zero,
			stashBalance: decimal.Zero,
			debts:        nil,
			profile:      nil,
			expectedStep: 0, // Step 0: Stabilize
		},
		{
			name: "spending more than earning — Step 0",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(200000),
				MonthlySpend:     decimal.NewFromInt(250000),
			},
			spendBalance: decimal.Zero,
			stashBalance: decimal.Zero,
			expectedStep: 0,
		},
		{
			name: "income covers expenses, no stash — Step 1",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			spendBalance: decimal.Zero,
			stashBalance: decimal.Zero,
			expectedStep: 1, // Starter Safety Net
		},
		{
			name: "stash covers 1 month — Step 2 if toxic debt exists",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			profile: &entities.FinancialProfile{
				MonthlyFixedCosts: decimal.NewFromInt(150000),
			},
			spendBalance:   decimal.Zero,
			stashBalance:    decimal.NewFromInt(150000), // covers 1 month
			debts: []entities.FinancialObligation{
				{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Credit Card", Amount: decimal.NewFromInt(50000), InterestRate: ptrDecimal(decimal.NewFromInt(25))},
			},
			expectedStep: 2, // Kill Toxic Debt
		},
		{
			name: "stash covers 1 month, no toxic debt — Step 3",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			profile: &entities.FinancialProfile{
				MonthlyFixedCosts: decimal.NewFromInt(150000),
			},
			spendBalance: decimal.Zero,
			stashBalance: decimal.NewFromInt(150000),
			debts: []entities.FinancialObligation{
				{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Family Loan", Amount: decimal.NewFromInt(100000), InterestRate: ptrDecimal(decimal.Zero)},
			},
			expectedStep: 3, // Full Safety Net
		},
		{
			name: "stash covers 3 months, no investment activity — Step 4",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			profile: &entities.FinancialProfile{
				MonthlyFixedCosts: decimal.NewFromInt(150000),
			},
			spendBalance:         decimal.Zero,
			stashBalance:          decimal.NewFromInt(450000), // 3 months
			hasInvestmentActivity: false,
			expectedStep:          4, // Build the Muscle
		},
		{
			name: "has investment activity, portfolio < 1x income — Step 5",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			profile: &entities.FinancialProfile{
				MonthlyFixedCosts: decimal.NewFromInt(150000),
			},
			spendBalance:         decimal.Zero,
			stashBalance:          decimal.NewFromInt(450000),
			hasInvestmentActivity: true,
			portfolioValue:       decimal.NewFromInt(1000000), // < 3.6M annual
			expectedStep:         5, // Accelerate
		},
		{
			name: "portfolio >= 1x annual income — Step 6",
			state: &entities.MiriamMoneyState{
				AvgMonthlyIncome: decimal.NewFromInt(300000),
				MonthlySpend:     decimal.NewFromInt(200000),
			},
			profile: &entities.FinancialProfile{
				MonthlyFixedCosts: decimal.NewFromInt(150000),
			},
			spendBalance:         decimal.Zero,
			stashBalance:          decimal.NewFromInt(450000),
			hasInvestmentActivity: true,
			portfolioValue:       decimal.NewFromInt(4000000), // > 3.6M annual
			expectedStep:         6, // Rich Life
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, _ := ClassifyFreedomStep(tt.state, tt.spendBalance, tt.stashBalance, tt.debts, tt.profile, tt.hasInvestmentActivity, tt.portfolioValue)
			if step != tt.expectedStep {
				t.Errorf("ClassifyFreedomStep() = %d, want %d", step, tt.expectedStep)
			}
		})
	}
}

func TestFreedomStepName(t *testing.T) {
	tests := []struct {
		step int
		want string
	}{
		{0, "Stabilize"},
		{1, "Starter Safety Net"},
		{2, "Kill Toxic Debt"},
		{3, "Full Safety Net"},
		{4, "Build the Muscle"},
		{5, "Accelerate"},
		{6, "Rich Life"},
		{-1, "Unknown"},
		{99, "Unknown"},
	}
	for _, tt := range tests {
		if got := FreedomStepName(tt.step); got != tt.want {
			t.Errorf("FreedomStepName(%d) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestFreedomStepNudge(t *testing.T) {
	for i := 0; i <= 6; i++ {
		nudge := FreedomStepNudge(i)
		if nudge == "" {
			t.Errorf("FreedomStepNudge(%d) returned empty string", i)
		}
	}
	// Out of range returns empty
	if nudge := FreedomStepNudge(-1); nudge != "" {
		t.Errorf("FreedomStepNudge(-1) = %q, want empty", nudge)
	}
}

func TestFilterToxicDebts(t *testing.T) {
	debts := []entities.FinancialObligation{
		{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Credit Card", Amount: decimal.NewFromInt(50000), InterestRate: ptrDecimal(decimal.NewFromInt(25))},
		{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Student Loan", Amount: decimal.NewFromInt(200000), InterestRate: ptrDecimal(decimal.NewFromInt(6))},
		{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Family Loan", Amount: decimal.NewFromInt(100000), InterestRate: ptrDecimal(decimal.Zero)},
		{Type: entities.ObligationTypeDebt, Status: entities.ObligationStatusActive, Name: "Bank Loan", Amount: decimal.NewFromInt(80000)}, // no rate → default 12%
	}

	toxic := filterToxicDebts(debts)
	if len(toxic) != 2 {
		t.Errorf("expected 2 toxic debts, got %d", len(toxic))
	}
}

func TestEstimateRateFromObligation(t *testing.T) {
	tests := []struct {
		name     string
		debt     entities.FinancialObligation
		wantRate float64
	}{
		{"explicit rate", entities.FinancialObligation{InterestRate: ptrDecimal(decimal.NewFromInt(15))}, 15},
		{"credit card default", entities.FinancialObligation{Name: "Visa Credit Card"}, 25},
		{"payday default", entities.FinancialObligation{Name: "Payday Loan"}, 400},
		{"student default", entities.FinancialObligation{Name: "Student Loan"}, 6},
		{"family default", entities.FinancialObligation{Name: "Family Loan"}, 0},
		{"generic default", entities.FinancialObligation{Name: "Some Loan"}, 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := estimateRateFromObligation(tt.debt)
			if !rate.Equals(decimal.NewFromFloat(tt.wantRate)) {
				t.Errorf("estimateRateFromObligation() = %s, want %v", rate.String(), tt.wantRate)
			}
		})
	}
}

func TestIsAuditReady(t *testing.T) {
	tests := []struct {
		name             string
		state            *entities.MiriamMoneyState
		hasBankStatement bool
		want             bool
	}{
		{"nil state, no statement", nil, false, false},
		{"1 month, no statement", &entities.MiriamMoneyState{ActiveMonths: 1}, false, false},
		{"3 months, no statement", &entities.MiriamMoneyState{ActiveMonths: 3}, false, true},
		{"nil state, has statement", nil, true, true},
		{"1 month, has statement", &entities.MiriamMoneyState{ActiveMonths: 1}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAuditReady(tt.state, tt.hasBankStatement); got != tt.want {
				t.Errorf("isAuditReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatFreedomStepsList(t *testing.T) {
	steps := formatFreedomStepsList(2)
	if len(steps) != 7 {
		t.Fatalf("expected 7 steps, got %d", len(steps))
	}
	// Steps 0 and 1 should be completed
	if steps[0]["status"] != "completed" {
		t.Errorf("step 0 status = %v, want completed", steps[0]["status"])
	}
	if steps[1]["status"] != "completed" {
		t.Errorf("step 1 status = %v, want completed", steps[1]["status"])
	}
	// Step 2 should be in_progress
	if steps[2]["status"] != "in_progress" {
		t.Errorf("step 2 status = %v, want in_progress", steps[2]["status"])
	}
	// Steps 3-6 should be locked
	if steps[3]["status"] != "locked" {
		t.Errorf("step 3 status = %v, want locked", steps[3]["status"])
	}
	if steps[6]["status"] != "locked" {
		t.Errorf("step 6 status = %v, want locked", steps[6]["status"])
	}
}

func TestFreedomStepsDefinitions(t *testing.T) {
	if len(FreedomSteps) != 7 {
		t.Fatalf("expected 7 FreedomSteps, got %d", len(FreedomSteps))
	}
	expectedNames := []string{"Stabilize", "Starter Safety Net", "Kill Toxic Debt", "Full Safety Net", "Build the Muscle", "Accelerate", "Rich Life"}
	for i, s := range FreedomSteps {
		if s.Name != expectedNames[i] {
			t.Errorf("FreedomSteps[%d].Name = %q, want %q", i, s.Name, expectedNames[i])
		}
		if s.Tagline == "" {
			t.Errorf("FreedomSteps[%d].Tagline is empty", i)
		}
		if s.Criteria == "" {
			t.Errorf("FreedomSteps[%d].Criteria is empty", i)
		}
		if s.CoachNudge == "" {
			t.Errorf("FreedomSteps[%d].CoachNudge is empty", i)
		}
	}
}

// ptrDecimal is a test helper that returns a pointer to a decimal.Decimal.
func ptrDecimal(d decimal.Decimal) *decimal.Decimal {
	return &d
}

// Ensure unused imports don't cause build failures
var _ = strings.Contains
var _ = time.Now
var _ = uuid.New

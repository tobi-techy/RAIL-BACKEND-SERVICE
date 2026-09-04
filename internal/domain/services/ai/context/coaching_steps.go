package context

import (
	"fmt"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// FreedomStep represents one step in the Rail Financial Freedom Steps
// framework — a 7-step progression (0–6).
type FreedomStep int

const (
	StepStabilize        FreedomStep = 0
	StepStarterSafetyNet FreedomStep = 1
	StepKillToxicDebt    FreedomStep = 2
	StepFullSafetyNet    FreedomStep = 3
	StepBuildTheMuscle   FreedomStep = 4
	StepAccelerate       FreedomStep = 5
	StepRichLife         FreedomStep = 6
)

// FreedomStepInfo describes one step.
type FreedomStepInfo struct {
	Step       FreedomStep
	Name       string
	Tagline    string
	Criteria   string
	CoachNudge string
}

// FreedomSteps is the canonical step list in order.
var FreedomSteps = []FreedomStepInfo{
	{Step: StepStabilize, Name: "Stabilize", Tagline: "Income beats expenses"},
	{Step: StepStarterSafetyNet, Name: "Starter Safety Net", Tagline: "1 month of expenses saved"},
	{Step: StepKillToxicDebt, Name: "Kill Toxic Debt", Tagline: "No debt above 10% APR"},
	{Step: StepFullSafetyNet, Name: "Full Safety Net", Tagline: "3–6 months in stash"},
	{Step: StepBuildTheMuscle, Name: "Build the Muscle", Tagline: "15-20% to investing on autopilot"},
	{Step: StepAccelerate, Name: "Accelerate", Tagline: "Max tax-advantaged, hyper-accumulate"},
	{Step: StepRichLife, Name: "Rich Life", Tagline: "Spend on what you love, give generously"},
}

// FreedomStepName returns the human-readable name for a step number.
func FreedomStepName(step int) string {
	if step < 0 || step >= len(FreedomSteps) {
		return ""
	}
	return FreedomSteps[step].Name
}

// FreedomStepNudge returns the coaching nudge for a step number.
func FreedomStepNudge(step int) string {
	if step < 0 || step >= len(FreedomSteps) {
		return ""
	}
	return FreedomSteps[step].CoachNudge
}

// IsAuditReady checks whether there's enough transaction data for an audit.
func IsAuditReady(state *entities.MiriamMoneyState, hasBankStatement bool) bool {
	if hasBankStatement {
		return true
	}
	if state != nil && state.ActiveMonths >= 3 {
		return true
	}
	return false
}

// EstimateRateFromObligation extracts the interest rate as a percentage.
func EstimateRateFromObligation(d entities.FinancialObligation) decimal.Decimal {
	if d.InterestRate != nil && d.InterestRate.IsPositive() {
		return *d.InterestRate
	}
	name := strings.ToLower(d.Name + " " + stringPtrValue(d.Counterparty))
	switch {
	case strings.Contains(name, "credit card") || containsWholeToken(name, "cc"):
		return decimal.NewFromInt(25)
	case strings.Contains(name, "payday"):
		return decimal.NewFromInt(400)
	case strings.Contains(name, "buy now") || strings.Contains(name, "bnpl"):
		return decimal.NewFromInt(20)
	case strings.Contains(name, "student") || strings.Contains(name, "education"):
		return decimal.NewFromInt(6)
	case strings.Contains(name, "family") || strings.Contains(name, "friend") || strings.Contains(name, "personal"):
		return decimal.Zero
	case strings.Contains(name, "mortgage"):
		return decimal.NewFromInt(5)
	default:
		return decimal.NewFromInt(12)
	}
}

func containsWholeToken(s, token string) bool {
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if field == token {
			return true
		}
	}
	return false
}

func stringPtrValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ClassifyFreedomStep determines which step the user is on from their financial state.
func ClassifyFreedomStep(
	state *entities.MiriamMoneyState,
	spendBalance, stashBalance decimal.Decimal,
	debts []entities.FinancialObligation,
	profile *entities.FinancialProfile,
	hasBankStatement bool,
	portfolioValue decimal.Decimal,
) (int, string) {
	if state == nil && spendBalance.IsZero() && stashBalance.IsZero() {
		return int(StepStabilize), "No financial data yet. Start by telling me your income and expenses."
	}

	income := decimal.Zero
	expenses := decimal.Zero
	if state != nil {
		income = state.AvgMonthlyIncome
		expenses = state.MonthlySpend
	}
	if profile != nil {
		if profile.MonthlyIncome.IsPositive() {
			income = profile.MonthlyIncome
		}
		if profile.MonthlyFixedCosts.IsPositive() {
			expenses = profile.MonthlyFixedCosts
		}
	}

	if income.IsZero() {
		return int(StepStabilize), "No income data yet. Track what comes in each month."
	}

	if expenses.GreaterThan(income) {
		return int(StepStabilize), fmt.Sprintf(
			"Spending ($%s) exceeds income ($%s). Kill subscriptions and trim until the gap closes.",
			expenses.StringFixed(0), income.StringFixed(0),
		)
	}

	if !stashBalance.IsPositive() {
		return int(StepStarterSafetyNet), fmt.Sprintf(
			"No stash yet. Aim for 1 month of expenses (~$%s) before anything else.",
			expenses.StringFixed(0),
		)
	}

	if len(debts) > 0 {
		toxic := false
		for _, d := range debts {
			rate := EstimateRateFromObligation(d)
			if rate.GreaterThan(decimal.NewFromFloat(toxicDebtThreshold)) {
				toxic = true
				break
			}
		}
		if toxic {
			return int(StepKillToxicDebt), fmt.Sprintf(
				"%d active debt(s) with rate above 10%%. Sprint phase: 80%% of discretionary to debt, 20%% to spending.",
				len(debts),
			)
		}
	}

	months := decimal.NewFromInt(3)
	if expenses.IsPositive() {
		ratio := stashBalance.Div(expenses)
		if ratio.LessThan(months) {
			return int(StepFullSafetyNet), fmt.Sprintf(
				"Stash covers %.1f months of expenses. Target 3-6 months.",
				ratio.InexactFloat64(),
			)
		}
	}

	if portfolioValue.IsZero() {
		return int(StepBuildTheMuscle), "Safety net in place. Automate 15-20% of income into investing."
	}

	if portfolioValue.GreaterThan(decimal.NewFromInt(0)) {
		return int(StepAccelerate), "Portfolio growing. Max tax-advantaged and pre-pay the future."
	}

	return int(StepRichLife), "Rich Life: You've built wealth. Spend on what you love, give generously, build legacy."
}

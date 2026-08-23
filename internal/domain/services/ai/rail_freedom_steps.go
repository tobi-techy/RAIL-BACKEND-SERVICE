package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// FreedomStep represents one step in the Rail Financial Freedom Steps
// framework — a 7-step progression (0–6) synthesizing the best of Dave Ramsey,
// Caleb Hammer, The Money Guys (Financial Order of Operations), and Ramit Sethi.
//
// Miriam never names these experts. She synthesizes their wisdom as her own
// coaching voice. The framework replaces Ramsey's 7 Baby Steps with a
// progression better suited to Rail's audience (18–30, Africa and diaspora):
// it starts with stabilisation (Hammer), separates toxic debt from all debt
// (Money Guys), adds incomemaxing (Hammer), and ends with a Rich Life
// (Sethi) rather than just "build wealth and give."
type FreedomStep int

const (
	// StepStabilize — income must exceed expenses before anything else.
	// Caleb Hammer's "stop the bleeding" principle. Track spending, kill
	// forgotten subscriptions, close the gap.
	StepStabilize FreedomStep = 0

	// StepStarterSafetyNet — save 1 month of basic expenses (min ₦150k / $1,000).
	// Ramsey Step 1 + Money Guys FOO Step 1. The "oh shit" fund.
	StepStarterSafetyNet FreedomStep = 1

	// StepKillToxicDebt — pay off all debt with interest > 10%.
	// Caleb Hammer + Money Guys FOO Step 3. User chooses avalanche (highest
	// rate first) or snowball (smallest balance first). Sprint phase: 80/20.
	StepKillToxicDebt FreedomStep = 2

	// StepFullSafetyNet — 3–6 months of expenses in stash.
	// Ramsey Step 3 + Money Guys FOO Step 4. Also: capture any employer match.
	StepFullSafetyNet FreedomStep = 3

	// StepBuildTheMuscle — automate investing at 15–20% of income.
	// Sethi "automate" + Ramsey Step 4. Consistency beats intensity.
	StepBuildTheMuscle FreedomStep = 4

	// StepAccelerate — max tax-advantaged accounts, hyper-accumulate,
	// pre-pay the future, and incomemax (increase income, not just cut).
	// Money Guys FOO Steps 5–6 + Hammer "incomemax."
	StepAccelerate FreedomStep = 5

	// StepRichLife — spend extravagantly on what you love, cut mercilessly
	// on what you don't, give generously. The money works for you now.
	// Sethi "Rich Life" + Ramsey Step 7. No terminal state — ongoing.
	StepRichLife FreedomStep = 6
)

// FreedomStepInfo describes one step for prompts, tool responses, and context.
type FreedomStepInfo struct {
	Step       FreedomStep
	Name       string
	Tagline    string // one-line summary
	Criteria   string // completion criteria in plain language
	CoachNudge string // what Miriam should steer toward when user is on this step
}

// FreedomSteps is the canonical step list in order.
var FreedomSteps = []FreedomStepInfo{
	{
		Step:       StepStabilize,
		Name:       "Stabilize",
		Tagline:    "Income must beat expenses before anything else matters.",
		Criteria:   "Monthly spend ≤ monthly income for 2 consecutive months.",
		CoachNudge: "Income is below (or barely above) expenses. Focus on closing the gap — track spending, cut forgotten subscriptions, find leaks. Don't suggest saving or investing yet. If they mention a purchase, ask if it's a need or a want. This is about survival, not growth.",
	},
	{
		Step:       StepStarterSafetyNet,
		Name:       "Starter Safety Net",
		Tagline:    "Save 1 month of expenses so life's surprises don't send you to debt.",
		Criteria:   "Stash balance ≥ 1 month of fixed costs (minimum ₦150k / $1,000).",
		CoachNudge: "User is building their starter safety net. Celebrate every deposit that grows the stash. Don't suggest investing yet. If they mention spending surplus, remind them the safety net comes first. Frame the stash as 'your oh-shit fund' — car breaks, phone cracks, medical bill — you won't reach for debt.",
	},
	{
		Step:       StepKillToxicDebt,
		Name:       "Kill Toxic Debt",
		Tagline:    "Destroy any debt charging you > 10% interest. Sprint phase: 80/20.",
		Criteria:   "Zero active debt with interest rate > 10%.",
		CoachNudge: "User is in the debt sprint. 80/20 spend/debt — every extra naira kills the target debt. Don't suggest investing beyond the starter safety net. Ask about interest rates when they add debts. Celebrate every payoff — the final toxic debt is BIG. Offer avalanche (highest rate first) or snowball (smallest balance first) based on their personality. Say 'sprint phase', never 'beans and rice'.",
	},
	{
		Step:       StepFullSafetyNet,
		Name:       "Full Safety Net",
		Tagline:    "3–6 months of expenses in your stash. This buys you time and peace.",
		Criteria:   "Stash balance ≥ 3× monthly fixed costs.",
		CoachNudge: "User is building their full safety net — 3 to 6 months of expenses. This is the foundation everything else sits on. The 70/30 split is doing the work; reinforce it. If they have an employer match anywhere, tell them not to leave free money on the table. Don't push investing yet — the safety net first.",
	},
	{
		Step:       StepBuildTheMuscle,
		Name:       "Build the Muscle",
		Tagline:    "Automate investing at 15–20% of income. Consistency beats intensity.",
		Criteria:   "3+ consecutive months of automated investing.",
		CoachNudge: "User is building the investing habit. The 70/30 split is already running; now layer in automated investing. Consistency over intensity — ₦10k/month beats ₦100k once a year. Help them set up an automation if they haven't. Don't try to time the market. If they ask about specific investments, offer options but never say 'buy X'.",
	},
	{
		Step:       StepAccelerate,
		Name:       "Accelerate",
		Tagline:    "Max out accounts, hyper-accumulate, pre-pay the future, and incomemax.",
		Criteria:   "Investment portfolio ≥ 1× annual income.",
		CoachNudge: "User is in the wealth-building phase. Push on two fronts: (1) max tax-advantaged accounts, hyper-accumulate in taxable investments, pre-pay mortgage/education; (2) incomemax — focus on increasing income (side hustle, skills, negotiate salary, start a business). Cutting has a floor; income has no ceiling. Help them think bigger.",
	},
	{
		Step:       StepRichLife,
		Name:       "Rich Life",
		Tagline:    "Spend extravagantly on what you love. Cut mercilessly on what you don't. Give generously.",
		Criteria:   "Ongoing — no terminal state.",
		CoachNudge: "User has built wealth. The goal now is to spend it on what they love, guilt-free. Conscious spending: be extravagant on the 2–3 things that matter most, cut everything else. Give generously. Build legacy. The money works for them now, not the other way around. Ask: 'What does your Rich Life look like?'",
	},
}

// FreedomStepName returns the human-readable name for a step number.
func FreedomStepName(step int) string {
	if step < 0 || step >= len(FreedomSteps) {
		return "Unknown"
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

// toxicDebtThreshold is the interest rate above which debt is considered "toxic"
// and prioritised in Step 2. Below this rate, debt can coexist with saving
// (Money Guys principle).
const toxicDebtThreshold = 10.0

// starterSafetyNetMin is the absolute minimum for the starter safety net,
// used when monthly fixed costs are unknown or very low.
var starterSafetyNetMin = decimal.NewFromInt(1000) // $1,000 USD

// fullSafetyNetMultiplier is how many months of fixed costs the full safety
// net must cover.
const fullSafetyNetMultiplier = 3

// ClassifyFreedomStep determines which step the user is on from their financial
// state. This is the core diagnosis engine used by both the coaching context
// injection (every turn) and the get_baby_steps tool.
//
// Parameters:
//   - state: MiriamMoneyState (may be nil for brand-new users)
//   - spendBalance, stashBalance: current wallet balances
//   - debts: active debt obligations
//   - financialProfile: user's saved profile (income, fixed costs, targets)
//   - hasInvestmentActivity: whether the user has 3+ months of investment history
//   - portfolioValue: total investment portfolio value
//
// Returns the step number (0–6) and a human-readable progress description.
func ClassifyFreedomStep(
	state *entities.MiriamMoneyState,
	spendBalance, stashBalance decimal.Decimal,
	debts []entities.FinancialObligation,
	financialProfile *entities.FinancialProfile,
	hasInvestmentActivity bool,
	portfolioValue decimal.Decimal,
) (step int, progress string) {
	monthlyIncome := decimal.Zero
	monthlySpend := decimal.Zero
	monthlyFixedCosts := decimal.Zero

	if state != nil {
		monthlyIncome = state.AvgMonthlyIncome
		monthlySpend = state.MonthlySpend
	}
	if financialProfile != nil {
		if financialProfile.MonthlyIncome.IsPositive() {
			monthlyIncome = financialProfile.MonthlyIncome
		}
		if financialProfile.MonthlyFixedCosts.IsPositive() {
			monthlyFixedCosts = financialProfile.MonthlyFixedCosts
		}
	}

	// If we have no income data at all, the user is brand new → Step 0.
	if monthlyIncome.IsZero() && monthlySpend.IsZero() {
		return int(StepStabilize), "No financial data yet. Start by telling me your income and expenses."
	}

	// Step 0: Stabilize — is income covering expenses?
	if monthlySpend.GreaterThan(monthlyIncome) {
		gap := monthlySpend.Sub(monthlyIncome)
		return int(StepStabilize), fmt.Sprintf(
			"Spending %.0f exceeds income %.0f by %.0f/month. Close this gap first.",
			monthlySpend.InexactFloat64(), monthlyIncome.InexactFloat64(), gap.InexactFloat64(),
		)
	}

	// Step 1: Starter Safety Net — does stash cover 1 month of fixed costs?
	starterTarget := starterSafetyNetMin
	if monthlyFixedCosts.IsPositive() {
		starterTarget = monthlyFixedCosts
		if starterTarget.LessThan(starterSafetyNetMin) {
			starterTarget = starterSafetyNetMin
		}
	}
	if stashBalance.LessThan(starterTarget) {
		pct := pctComplete(stashBalance, starterTarget)
		return int(StepStarterSafetyNet), fmt.Sprintf(
			"Starter Safety Net: %.0f saved of %.0f target (%.0f%%).",
			stashBalance.InexactFloat64(), starterTarget.InexactFloat64(), pct,
		)
	}

	// Step 2: Kill Toxic Debt — any debt with interest > 10%?
	toxicDebts := filterToxicDebts(debts)
	if len(toxicDebts) > 0 {
		totalToxic := decimal.Zero
		for _, d := range toxicDebts {
			totalToxic = totalToxic.Add(d.Amount)
		}
		return int(StepKillToxicDebt), fmt.Sprintf(
			"Kill Toxic Debt: %d debt(s) at >10%% interest, totaling %.0f. Sprint phase: 80/20.",
			len(toxicDebts), totalToxic.InexactFloat64(),
		)
	}

	// Step 3: Full Safety Net — does stash cover 3 months of fixed costs?
	fullTarget := starterTarget.Mul(decimal.NewFromInt(fullSafetyNetMultiplier))
	if monthlyFixedCosts.IsPositive() {
		fullTarget = monthlyFixedCosts.Mul(decimal.NewFromInt(fullSafetyNetMultiplier))
	}
	if stashBalance.LessThan(fullTarget) {
		pct := pctComplete(stashBalance, fullTarget)
		return int(StepFullSafetyNet), fmt.Sprintf(
			"Full Safety Net: %.0f saved of %.0f target (%.0f%%). 3 months of expenses.",
			stashBalance.InexactFloat64(), fullTarget.InexactFloat64(), pct,
		)
	}

	// Step 4: Build the Muscle — has the user been investing consistently?
	if !hasInvestmentActivity {
		return int(StepBuildTheMuscle), "Build the Muscle: Start automated investing at 15–20% of income. Consistency beats intensity."
	}

	// Step 5: Accelerate — is portfolio ≥ 1× annual income?
	annualIncome := monthlyIncome.Mul(decimal.NewFromInt(12))
	if portfolioValue.LessThan(annualIncome) {
		pct := pctComplete(portfolioValue, annualIncome)
		return int(StepAccelerate), fmt.Sprintf(
			"Accelerate: Portfolio %.0f of %.0f target (1× annual income, %.0f%%). Incomemax + hyper-accumulate.",
			portfolioValue.InexactFloat64(), annualIncome.InexactFloat64(), pct,
		)
	}

	// Step 6: Rich Life
	return int(StepRichLife), "Rich Life: You've built wealth. Spend on what you love, give generously, build legacy."
}

// filterToxicDebts returns debts with interest rates above the toxic threshold.
func filterToxicDebts(debts []entities.FinancialObligation) []entities.FinancialObligation {
	var toxic []entities.FinancialObligation
	for _, d := range debts {
		if d.Type != entities.ObligationTypeDebt || d.Status != entities.ObligationStatusActive {
			continue
		}
		rate := estimateRateFromObligation(d)
		if rate.GreaterThan(decimal.NewFromFloat(toxicDebtThreshold)) {
			toxic = append(toxic, d)
		}
	}
	return toxic
}

// estimateRateFromObligation extracts the interest rate as a percentage from
// an obligation, falling back to defaults by debt name/counterparty when not
// explicitly set. Returns a percentage value (25.0 = 25%).
func estimateRateFromObligation(d entities.FinancialObligation) decimal.Decimal {
	if d.InterestRate != nil && d.InterestRate.IsPositive() {
		return *d.InterestRate // already stored as a percentage (25.0 = 25%)
	}
	// Default estimates by name keywords — returned as percentages
	name := strings.ToLower(d.Name + " " + stringPtrValue(d.Counterparty))
	switch {
	case strings.Contains(name, "credit card") || strings.Contains(name, "cc"):
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

func pctComplete(current, target decimal.Decimal) float64 {
	if target.IsZero() {
		return 0
	}
	pct := current.Div(target).Mul(decimal.NewFromInt(100)).InexactFloat64()
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// formatFreedomStepsList returns a compact list of all steps with their
// current status for tool responses.
func formatFreedomStepsList(currentStep int) []map[string]interface{} {
	result := make([]map[string]interface{}, len(FreedomSteps))
	for i, s := range FreedomSteps {
		status := "locked"
		if i < currentStep {
			status = "completed"
		} else if i == currentStep {
			status = "in_progress"
		}
		result[i] = map[string]interface{}{
			"step":        i,
			"name":        s.Name,
			"tagline":     s.Tagline,
			"criteria":    s.Criteria,
			"status":      status,
			"coach_nudge": s.CoachNudge,
		}
	}
	return result
}

// isAuditReady checks whether there's enough transaction data for a
// Caleb Hammer-style spending audit.
func isAuditReady(state *entities.MiriamMoneyState, hasBankStatement bool) bool {
	if hasBankStatement {
		return true
	}
	if state != nil && state.ActiveMonths >= 3 {
		return true
	}
	return false
}

// _ time import guard — time is used in future coaching context expansion.
var _ = time.Now

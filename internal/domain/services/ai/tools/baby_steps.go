package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

func RegisterBabyStepsTools(r *Registry) {
	r.Register(NewTool(
		"get_baby_steps",
		"Get the user's Financial Freedom Steps plan: current step (0-6), debt payoff order (snowball or avalanche), interest costs, payoff timeline, sprint phase status, and coaching nudge. Use when the user asks about debt, debt payoff, snowball, avalanche, baby steps, freedom steps, sprint phase, financial freedom, or becoming debt-free. Always-on — available in every conversation.",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ObligationManager == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"has_debts":    false,
					"current_step": 0,
					"step_name":    "Stabilize",
					"message":      "No debt tracking available yet. Add your debts and I'll build your payoff plan.",
					"steps":        freedomStepsList(0),
				}}, nil
			}

			obligations, err := deps.ObligationManager.List(ctx, userID, "active", "")
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("list obligations: %v", err)}, nil
			}

			var debts []entities.FinancialObligation
			for _, ob := range obligations {
				if ob.Type == entities.ObligationTypeDebt && ob.Status == entities.ObligationStatusActive {
					debts = append(debts, ob)
				}
			}

			if len(debts) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"has_debts":    false,
					"current_step": 1,
					"step_name":    "Starter Safety Net",
					"message":      "No active debts found. You're ahead — let's build that starter safety net.",
					"steps":        freedomStepsList(1),
				}}, nil
			}

			// Sort by snowball (smallest first) for the priority display.
			sortDebtsSnowball(debts)
			snowball := calculateSnowball(debts)

			// Also sort by avalanche (highest interest first) for comparison.
			avalancheOrder := make([]entities.FinancialObligation, len(debts))
			copy(avalancheOrder, debts)
			sortDebtsAvalanche(avalancheOrder)

			debtList := make([]map[string]interface{}, 0, len(debts))
			for i, d := range debts {
				item := map[string]interface{}{
					"name":              d.Name,
					"balance":           d.Amount.StringFixed(2),
					"minimum_payment":   calculateMinimumPayment(d).StringFixed(2),
					"interest_rate":     formatInterestRate(d),
					"cadence":           d.Cadence,
					"snowball_priority": i + 1,
				}
				if d.InterestRate != nil {
					item["interest_rate_pct"] = d.InterestRate.StringFixed(1)
				}
				// Mark toxic debts (>10% interest)
				rate := estimateInterestRate(d)
				if rate.Mul(decimal.NewFromInt(100)).GreaterThan(decimal.NewFromInt(10)) {
					item["is_toxic"] = true
				}
				debtList = append(debtList, item)
			}

			result := map[string]interface{}{
				"has_debts":         true,
				"total_debt":        snowball.TotalDebt.StringFixed(2),
				"debt_count":        len(debts),
				"debts":             debtList,
				"current_step":      2,
				"step_name":         "Kill Toxic Debt",
				"step_status":       "in_progress",
				"snowball_order":    "smallest_balance_first",
				"avalanche_order":   "highest_interest_first",
				"debt_method":       "snowball",
				"total_interest":    snowball.TotalInterestCost.StringFixed(2),
				"months_to_freedom": snowball.MonthsToFreedom,
				"next_target":       snowball.NextTarget,
				"steps":             freedomStepsList(2),
				"snowball": map[string]interface{}{
					"smallest_debt":     snowball.NextTarget,
					"freed_payment":     snowball.FreedPayment.StringFixed(2),
					"projected_savings": snowball.InterestSaved.StringFixed(2),
					"sprint_allocation": "80/20 spend/debt during sprint",
					"post_sprint":       "70/30 spend/stash after all debts cleared",
				},
				"coaching_nudge": "User is in the debt sprint. 80/20 spend/debt -- every extra naira kills the target debt. Don't suggest investing beyond the starter safety net. Celebrate every payoff.",
			}

			return &core.ToolResult{Data: result}, nil
		},
	))
}

// freedomStepsList returns the 7-step framework with status for tool responses.
func freedomStepsList(currentStep int) []map[string]interface{} {
	steps := []struct {
		name    string
		tagline string
	}{
		{"Stabilize", "Income must beat expenses before anything else matters."},
		{"Starter Safety Net", "Save 1 month of expenses so life's surprises don't send you to debt."},
		{"Kill Toxic Debt", "Destroy any debt charging you > 10% interest. Sprint phase: 80/20."},
		{"Full Safety Net", "3-6 months of expenses in your stash. This buys you time and peace."},
		{"Build the Muscle", "Automate investing at 15-20% of income. Consistency beats intensity."},
		{"Accelerate", "Max out accounts, hyper-accumulate, pre-pay the future, and incomemax."},
		{"Rich Life", "Spend extravagantly on what you love. Cut mercilessly on what you don't. Give generously."},
	}
	result := make([]map[string]interface{}, len(steps))
	for i, s := range steps {
		status := "locked"
		if i < currentStep {
			status = "completed"
		} else if i == currentStep {
			status = "in_progress"
		}
		result[i] = map[string]interface{}{
			"step":    i,
			"name":    s.name,
			"tagline": s.tagline,
			"status":  status,
		}
	}
	return result
}

// snowballResult holds the calculated snowball plan.
type snowballResult struct {
	TotalDebt         decimal.Decimal
	TotalInterestCost decimal.Decimal
	InterestSaved     decimal.Decimal
	MonthsToFreedom   int
	NextTarget        string
	FreedPayment      decimal.Decimal
}

func calculateSnowball(debts []entities.FinancialObligation) snowballResult {
	result := snowballResult{}
	if len(debts) == 0 {
		return result
	}

	for _, d := range debts {
		result.TotalDebt = result.TotalDebt.Add(d.Amount)
	}

	smallest := debts[0]
	result.NextTarget = fmt.Sprintf("%s (%s)", smallest.Name, smallest.Amount.StringFixed(2))
	result.FreedPayment = calculateMinimumPayment(smallest)

	for _, d := range debts {
		rate := estimateInterestRate(d)
		annualInterest := d.Amount.Mul(rate)
		result.TotalInterestCost = result.TotalInterestCost.Add(annualInterest)
	}

	result.InterestSaved = result.TotalInterestCost.Mul(decimal.NewFromFloat(0.30))

	totalMinPayment := decimal.Zero
	for _, d := range debts {
		totalMinPayment = totalMinPayment.Add(calculateMinimumPayment(d))
	}
	if totalMinPayment.IsPositive() {
		result.MonthsToFreedom = int(result.TotalDebt.Div(totalMinPayment).Mul(decimal.NewFromFloat(1.5)).InexactFloat64())
		if result.MonthsToFreedom < 3 {
			result.MonthsToFreedom = 3
		}
		if result.MonthsToFreedom > 60 {
			result.MonthsToFreedom = 60
		}
	}

	return result
}

func calculateMinimumPayment(d entities.FinancialObligation) decimal.Decimal {
	if d.Amount.IsZero() {
		return decimal.Zero
	}
	minPct := d.Amount.Mul(decimal.NewFromFloat(0.02))
	floor := decimal.NewFromInt(10)
	if minPct.LessThan(floor) {
		minPct = floor
	}
	return minPct.Round(2)
}

func estimateInterestRate(d entities.FinancialObligation) decimal.Decimal {
	if d.InterestRate != nil && d.InterestRate.IsPositive() {
		return d.InterestRate.Div(decimal.NewFromInt(100))
	}
	name := strings.ToLower(d.Name + " " + coalesceStr(d.Counterparty))
	switch {
	case strings.Contains(name, "credit card") || strings.Contains(name, "cc"):
		return decimal.NewFromFloat(0.25)
	case strings.Contains(name, "student") || strings.Contains(name, "education"):
		return decimal.NewFromFloat(0.06)
	case strings.Contains(name, "family") || strings.Contains(name, "friend") || strings.Contains(name, "personal"):
		return decimal.NewFromFloat(0.0)
	case strings.Contains(name, "loan") || strings.Contains(name, "bnpl"):
		return decimal.NewFromFloat(0.18)
	case strings.Contains(name, "mortgage") || strings.Contains(name, "rent"):
		return decimal.NewFromFloat(0.0)
	default:
		return decimal.NewFromFloat(0.12)
	}
}

func formatInterestRate(d entities.FinancialObligation) string {
	if d.InterestRate != nil && d.InterestRate.IsPositive() {
		return fmt.Sprintf("%s%% annual", d.InterestRate.StringFixed(1))
	}
	rate := estimateInterestRate(d)
	pct := rate.Mul(decimal.NewFromInt(100))
	return fmt.Sprintf("~%s%% estimated", pct.StringFixed(0))
}

func coalesceStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sortDebtsSnowball(debts []entities.FinancialObligation) {
	for i := 0; i < len(debts); i++ {
		for j := i + 1; j < len(debts); j++ {
			if debts[j].Amount.LessThan(debts[i].Amount) {
				debts[i], debts[j] = debts[j], debts[i]
			}
		}
	}
}

// sortDebtsAvalanche sorts debts by interest rate, highest first.
func sortDebtsAvalanche(debts []entities.FinancialObligation) {
	for i := 0; i < len(debts); i++ {
		for j := i + 1; j < len(debts); j++ {
			rateI := estimateInterestRate(debts[i])
			rateJ := estimateInterestRate(debts[j])
			if rateJ.GreaterThan(rateI) {
				debts[i], debts[j] = debts[j], debts[i]
			}
		}
	}
}

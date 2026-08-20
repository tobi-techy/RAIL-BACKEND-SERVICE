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
		"Get the user's Baby Steps debt-free coaching plan: current step, debt snowball order, interest costs, payoff timeline, and sprint phase status. Use when the user asks about debt, debt payoff, debt snowball, baby steps, sprint phase, or becoming debt-free. Always-on — available in every conversation.",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ObligationManager == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"has_debts": false,
					"message":   "No debt tracking available yet. Add your debts and I'll build your payoff plan.",
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
					"has_debts": false,
					"step":      1,
					"message":   "No active debts found. You're ahead of the game — let's build that emergency fund.",
				}}, nil
			}

			sortDebtsSnowball(debts)
			snowball := calculateSnowball(debts)

			debtList := make([]map[string]interface{}, 0, len(debts))
			for i, d := range debts {
				item := map[string]interface{}{
					"name":            d.Name,
					"balance":         d.Amount.StringFixed(2),
					"minimum_payment": calculateMinimumPayment(d).StringFixed(2),
					"interest_rate":   formatInterestRate(d),
					"cadence":         d.Cadence,
					"priority":        i + 1,
				}
				if d.InterestRate != nil {
					item["interest_rate_pct"] = d.InterestRate.StringFixed(1)
				}
				debtList = append(debtList, item)
			}

			result := map[string]interface{}{
				"has_debts":         true,
				"total_debt":        snowball.TotalDebt.StringFixed(2),
				"debt_count":        len(debts),
				"debts":             debtList,
				"current_step":      2,
				"step_status":       "in_progress",
				"snowball_order":    "smallest_balance_first",
				"total_interest":    snowball.TotalInterestCost.StringFixed(2),
				"months_to_freedom": snowball.MonthsToFreedom,
				"next_target":       snowball.NextTarget,
				"snowball": map[string]interface{}{
					"smallest_debt":     snowball.NextTarget,
					"freed_payment":     snowball.FreedPayment.StringFixed(2),
					"projected_savings": snowball.InterestSaved.StringFixed(2),
					"sprint_allocation": "80/20 spend/debt during sprint",
					"post_sprint":       "70/30 spend/stash after all debts cleared",
				},
			}

			return &core.ToolResult{Data: result}, nil
		},
	))
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

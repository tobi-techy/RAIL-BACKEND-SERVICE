package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetBabySteps = "get_baby_steps"

func BabyStepsTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetBabySteps,
			Description: "Get the user's Baby Steps debt-free coaching plan: current step, debt snowball order, interest costs, payoff timeline, and sprint phase status. Use when the user asks about debt, debt payoff, debt snowball, baby steps, sprint phase, or becoming debt-free. Always-on — available in every conversation.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
	}
}

// ObligationLister lists active obligations for a user.
type ObligationLister interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

func (o *AgentAdapter) executeBabySteps(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.obligations == nil {
		return map[string]interface{}{
			"has_debts": false,
			"message":   "No debt tracking available yet. Add your debts and I'll build your payoff plan.",
		}, nil
	}

	obligations, err := o.obligations.ListActive(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list obligations for baby steps: %w", err)
	}

	// Filter to debt-type obligations
	var debts []entities.FinancialObligation
	for _, ob := range obligations {
		if ob.Type == entities.ObligationTypeDebt && ob.Status == entities.ObligationStatusActive {
			debts = append(debts, ob)
		}
	}

	if len(debts) == 0 {
		return map[string]interface{}{
			"has_debts": false,
			"step":      1,
			"message":   "No active debts found. You're ahead of the game — let's build that emergency fund.",
			"display":   map[string]interface{}{},
		}, nil
	}

	// Sort debts smallest to largest (snowball order)
	sortDebtsSnowball(debts)

	// Calculate snowball math
	snowball := calculateSnowball(debts)

	// Determine current step
	step := 2 // In debt = step 2
	stepStatus := "in_progress"

	// Check if emergency fund exists (step 1)
	_, stash, _ := o.currentBalances(ctx, userID)
	profile := o.getProfile(ctx, userID)
	emergencyTarget := calculateEmergencyTarget(profile)
	emergencySaved := stash // Simplified: stash = emergency fund
	emergencyFunded := emergencySaved.GreaterThanOrEqual(emergencyTarget)

	if !emergencyFunded {
		step = 1
		stepStatus = "in_progress"
	} else if len(debts) == 0 {
		step = 3
		stepStatus = "in_progress"
	}

	// Build debt list with display amounts
	display := userDisplayContext(ctx, o, userID)
	debtList := make([]map[string]interface{}, 0, len(debts))
	for i, d := range debts {
		item := map[string]interface{}{
			"name":            d.Name,
			"balance":         d.Amount.StringFixed(2),
			"minimum_payment": calculateMinimumPayment(d).StringFixed(2),
			"interest_rate":   formatInterestRate(d),
			"cadence":         d.Cadence,
			"priority":        i + 1, // snowball order
		}
		if d.InterestRate != nil {
			item["interest_rate_pct"] = d.InterestRate.StringFixed(1)
		}
		debtList = append(debtList, item)
	}

	// Build response
	result := map[string]interface{}{
		"has_debts":         true,
		"total_debt":        snowball.TotalDebt.StringFixed(2),
		"debt_count":        len(debts),
		"debts":             debtList,
		"current_step":      step,
		"step_status":       stepStatus,
		"snowball_order":    "smallest_balance_first",
		"total_interest":    snowball.TotalInterestCost.StringFixed(2),
		"months_to_freedom": snowball.MonthsToFreedom,
		"next_target":       snowball.NextTarget,
		"display":           display.displayMap(),
	}

	// Emergency fund context
	if step == 1 {
		result["emergency_fund"] = map[string]interface{}{
			"target":   emergencyTarget.StringFixed(2),
			"saved":    emergencySaved.StringFixed(2),
			"progress": emergencyProgressPct(emergencySaved, emergencyTarget),
			"status":   "in_progress",
		}
		result["message"] = fmt.Sprintf("Step 1: Build a starter emergency fund of %s before attacking debt.", display.displayAmount(emergencyTarget))
	} else {
		result["emergency_fund"] = map[string]interface{}{
			"target":  emergencyTarget.StringFixed(2),
			"saved":   emergencySaved.StringFixed(2),
			"status":  "funded",
			"message": "Emergency fund is set. Full speed on debt.",
		}
	}

	// Snowball detail
	result["snowball"] = map[string]interface{}{
		"smallest_debt":        snowball.NextTarget,
		"freed_payment":        snowball.FreedPayment.StringFixed(2),
		"projected_savings":    snowball.InterestSaved.StringFixed(2),
		"sprint_allocation":    "80/20 spend/debt during sprint",
		"post_sprint":          "70/30 spend/stash after all debts cleared",
	}

	return result, nil
}

type snowballResult struct {
	TotalDebt        decimal.Decimal
	TotalInterestCost decimal.Decimal
	InterestSaved    decimal.Decimal
	MonthsToFreedom  int
	NextTarget       string
	FreedPayment     decimal.Decimal
}

func calculateSnowball(debts []entities.FinancialObligation) snowballResult {
	result := snowballResult{}
	if len(debts) == 0 {
		return result
	}

	// Total debt
	for _, d := range debts {
		result.TotalDebt = result.TotalDebt.Add(d.Amount)
	}

	// Next target = smallest debt
 smallest := debts[0]
	result.NextTarget = fmt.Sprintf("%s (%s)", smallest.Name, smallest.Amount.StringFixed(2))
	result.FreedPayment = calculateMinimumPayment(smallest)

	// Estimate total interest cost (12-month projection)
	for _, d := range debts {
		rate := estimateInterestRate(d)
		// Simple interest estimate for 12 months
		annualInterest := d.Amount.Mul(rate)
		result.TotalInterestCost = result.TotalInterestCost.Add(annualInterest)
	}

	// Estimate interest saved by snowball vs minimum-only
	// Snowball saves roughly 30% of total interest by paying off early
	result.InterestSaved = result.TotalInterestCost.Mul(decimal.NewFromFloat(0.30))

	// Months to freedom estimate
	totalMinPayment := decimal.Zero
	for _, d := range debts {
		totalMinPayment = totalMinPayment.Add(calculateMinimumPayment(d))
	}
	if totalMinPayment.IsPositive() {
		// Rough estimate: total debt / total minimum payments * 1.5 (for interest)
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
	// 2% of balance with $10/₦1500 floor
	minPct := d.Amount.Mul(decimal.NewFromFloat(0.02))
	floor := decimal.NewFromInt(10) // USD equivalent
	if minPct.LessThan(floor) {
		minPct = floor
	}
	return minPct.Round(2)
}

func estimateInterestRate(d entities.FinancialObligation) decimal.Decimal {
	if d.InterestRate != nil && d.InterestRate.IsPositive() {
		return d.InterestRate.Div(decimal.NewFromInt(100)) // Convert from percentage to decimal
	}
	// Smart defaults based on name/counterparty
	name := strings.ToLower(d.Name + " " + coalesceStr(d.Counterparty))
	switch {
	case strings.Contains(name, "credit card") || strings.Contains(name, "cc"):
		return decimal.NewFromFloat(0.25) // 25% annual
	case strings.Contains(name, "student") || strings.Contains(name, "education"):
		return decimal.NewFromFloat(0.06)
	case strings.Contains(name, "family") || strings.Contains(name, "friend") || strings.Contains(name, "personal"):
		return decimal.NewFromFloat(0.0)
	case strings.Contains(name, "loan") || strings.Contains(name, "bnpl"):
		return decimal.NewFromFloat(0.18)
	case strings.Contains(name, "mortgage") || strings.Contains(name, "rent"):
		return decimal.NewFromFloat(0.0)
	default:
		return decimal.NewFromFloat(0.12) // 12% fallback
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
	// Bubble sort smallest to largest by Amount
	for i := 0; i < len(debts); i++ {
		for j := i + 1; j < len(debts); j++ {
			if debts[j].Amount.LessThan(debts[i].Amount) {
				debts[i], debts[j] = debts[j], debts[i]
			}
		}
	}
}

func calculateEmergencyTarget(profile *entities.FinancialProfile) decimal.Decimal {
	if profile == nil {
		return decimal.NewFromInt(1000) // Default $1000
	}
	// PPP-scaled starter emergency fund: 1 month of local expenses
	monthlyIncome := profile.MonthlyIncome
	if monthlyIncome.IsZero() {
		monthlyIncome = profile.MonthlyFixedCosts
	}
	if monthlyIncome.IsZero() {
		return decimal.NewFromInt(1000)
	}
	return monthlyIncome
}

func emergencyProgressPct(saved, target decimal.Decimal) string {
	if target.IsZero() {
		return "0"
	}
	pct := saved.Div(target).Mul(decimal.NewFromInt(100))
	if pct.GreaterThan(decimal.NewFromInt(100)) {
		pct = decimal.NewFromInt(100)
	}
	return pct.StringFixed(0)
}

func (o *AgentAdapter) getProfile(ctx context.Context, userID uuid.UUID) *entities.FinancialProfile {
	if o.financialProfile == nil {
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	profile, _ := o.financialProfile.GetByUserID(fetchCtx, userID)
	return profile
}

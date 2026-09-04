package ai

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	"github.com/shopspring/decimal"
)

// GetConsciousSpendingSnapshot assembles the observed four-number view used by
// weekly coaching from categorized transactions. The interactive build tool
// intentionally uses different, user-supplied planning inputs.
func (o *AgentAdapter) GetConsciousSpendingSnapshot(ctx context.Context, userID uuid.UUID) consciousspending.Snapshot {
	var in consciousspending.SnapshotInput

	if o.financialProfile != nil {
		if profile, err := o.financialProfile.GetByUserID(ctx, userID); err == nil && profile != nil {
			in.Currency = profile.PrimaryCurrency
			if profile.MonthlyIncome.IsPositive() {
				in.TakeHomeIncome = consciousspending.NewObservedAmount(profile.MonthlyIncome, "financial_profile", "medium")
			}
		}
	}

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if o.monoAnalysis != nil && (in.Currency == "" || strings.EqualFold(in.Currency, "NGN")) {
		if analysis, err := o.monoAnalysis.GetSpendingAnalysis(ctx, userID, now.Day()); err == nil &&
			analysis != nil && analysis.TotalCredits > 0 {
			in.Currency = "NGN"
			in.TakeHomeIncome = consciousspending.NewObservedAmount(decimal.NewFromInt(analysis.TotalCredits).Shift(-2), "mono_recent_period", "high")
			fixed, investments, guiltFree, complete := classifyMonoCategories(analysis.ByCategory)
			in.FixedCosts = consciousspending.NewObservedAmount(fixed, "mono_categories", "medium")
			in.Investments = consciousspending.NewObservedAmount(investments, "mono_categories", "medium")
			if complete {
				in.GuiltFreeSpending = consciousspending.NewObservedAmount(guiltFree, "mono_categories", "medium")
				remainder := in.TakeHomeIncome.Amount.Sub(fixed).Sub(investments).Sub(guiltFree)
				if !remainder.IsNegative() {
					in.Savings = consciousspending.NewObservedAmount(remainder, "mono_remainder", "medium")
				}
			}
			return consciousspending.CalculateSnapshot(in)
		}
	}
	if o.spending != nil {
		if flow, err := o.spending.GetMoneyFlow(ctx, userID, start, now); err == nil && flow != nil &&
			flow.TotalDeposits.IsPositive() {
			if !in.TakeHomeIncome.Known {
				in.TakeHomeIncome = consciousspending.NewObservedAmount(flow.TotalDeposits, "month_money_flow", "medium")
			}
			if summary, summaryErr := o.spending.GetSummary(ctx, userID, start, now); summaryErr == nil && summary != nil {
				fixed, investments, guiltFree, complete := classifyRailCategories(summary.Categories)
				in.FixedCosts = consciousspending.NewObservedAmount(fixed, "rail_categories", "medium")
				in.Investments = consciousspending.NewObservedAmount(investments, "rail_categories", "medium")
				if complete {
					in.GuiltFreeSpending = consciousspending.NewObservedAmount(guiltFree, "rail_categories", "medium")
					remainder := in.TakeHomeIncome.Amount.Sub(fixed).Sub(investments).Sub(guiltFree)
					if !remainder.IsNegative() {
						in.Savings = consciousspending.NewObservedAmount(remainder, "rail_remainder", "medium")
					}
				}
			}
		}
	}
	return consciousspending.CalculateSnapshot(in)
}

func classifyMonoCategories(categories []entities.MonoCategoryBreakdown) (decimal.Decimal, decimal.Decimal, decimal.Decimal, bool) {
	fixed, investments, guiltFree := decimal.Zero, decimal.Zero, decimal.Zero
	complete := true
	for _, category := range categories {
		amount := decimal.NewFromInt(category.Amount).Shift(-2)
		if !addCSPCategory(category.Category, amount, &fixed, &investments, &guiltFree) {
			complete = false
		}
	}
	return fixed, investments, guiltFree, complete
}

func classifyRailCategories(categories []entities.SpendingByCategory) (decimal.Decimal, decimal.Decimal, decimal.Decimal, bool) {
	fixed, investments, guiltFree := decimal.Zero, decimal.Zero, decimal.Zero
	complete := true
	for _, category := range categories {
		if !addCSPCategory(category.Category, category.Total, &fixed, &investments, &guiltFree) {
			complete = false
		}
	}
	return fixed, investments, guiltFree, complete
}

func addCSPCategory(category string, amount decimal.Decimal, fixed, investments, guiltFree *decimal.Decimal) bool {
	label := strings.ToLower(category)
	switch {
	case cspContainsAny(label, "rent", "mortgage", "housing", "utility", "utilities", "electric", "internet",
		"insurance", "school", "tuition", "transport", "grocer", "health", "medical", "airtime", "bill",
		"fee", "charge", "tax", "loan", "debt"):
		*fixed = fixed.Add(amount)
	case cspContainsAny(label, "invest", "broker", "stock", "fund", "pension", "retirement", "crypto"):
		*investments = investments.Add(amount)
	case cspContainsAny(label, "food", "dining", "restaurant", "shopping", "retail", "entertainment",
		"travel", "leisure", "personal", "subscription", "card payment"):
		*guiltFree = guiltFree.Add(amount)
	default:
		return false
	}
	return true
}

func cspContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

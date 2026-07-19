package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Tool name for recurring expenses.
const ToolGetRecurringExpenses = "get_recurring_expenses"

// RecurringExpense represents a detected recurring merchant.
type RecurringExpense struct {
	Merchant  string          `json:"merchant"`
	Frequency string          `json:"frequency"`
	AvgAmount decimal.Decimal `json:"avg_amount"`
	Total     decimal.Decimal `json:"total"`
	FirstSeen string          `json:"first_seen"`
	LastSeen  string          `json:"last_seen"`
	Count     int             `json:"count"`
}

// RecurringExpenseDetector detects recurring expenses from all spending sources.
type RecurringExpenseDetector interface {
	DetectRecurring(ctx context.Context, userID uuid.UUID) ([]RecurringExpense, error)
}

// SetRecurringDetector sets the recurring expense detector.
func (o *AgentAdapter) SetRecurringDetector(d RecurringExpenseDetector) {
	o.recurringDetector = d
}

// RecurringExpenseTool returns the tool definition.
func RecurringExpenseTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetRecurringExpenses,
		Description: "Analyze all spending sources (card transactions, P2P transfers, withdrawals, receipts) to detect recurring expenses and regular outflows. Use when user asks about recurring spending, subscriptions, regular expenses, or repeated transfers.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *AgentAdapter) executeRecurringExpenses(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.recurringDetector == nil {
		return map[string]interface{}{"error": "recurring expense detection not available"}, nil
	}
	expenses, err := o.recurringDetector.DetectRecurring(ctx, userID)
	if err != nil {
		o.logger.Warn("recurring expense detection failed", zap.Error(err))
		return map[string]interface{}{"available": false, "error": "Could not load recurring expenses right now. Try again shortly."}, nil
	}

	totalMonthly := decimal.Zero
	for _, e := range expenses {
		if e.Frequency == "weekly" {
			totalMonthly = totalMonthly.Add(e.AvgAmount.Mul(decimal.NewFromFloat(4.33)))
		} else {
			totalMonthly = totalMonthly.Add(e.AvgAmount)
		}
	}

	items := make([]map[string]interface{}, len(expenses))
	for i, e := range expenses {
		items[i] = map[string]interface{}{
			"merchant":   e.Merchant,
			"frequency":  e.Frequency,
			"avg_amount": e.AvgAmount.StringFixed(2),
			"total":      e.Total.StringFixed(2),
			"first_seen": e.FirstSeen,
			"last_seen":  e.LastSeen,
			"count":      e.Count,
		}
	}

	// Enrich recurring expense entries with plain descriptions and context
	if enrichmentMap := enrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		for _, item := range items {
			enrichMerchantEntry(item, enrichmentMap)
		}
	}

	return map[string]interface{}{
		"recurring_expenses":      items,
		"total_recurring_monthly": totalMonthly.StringFixed(2),
		"insight":                 fmt.Sprintf("You have %d recurring expenses totaling $%s/month", len(expenses), totalMonthly.StringFixed(2)),
	}, nil
}

package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
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

// RecurringExpenseDetector detects recurring expenses from receipts and card transactions.
type RecurringExpenseDetector interface {
	DetectRecurring(ctx context.Context, userID uuid.UUID) ([]RecurringExpense, error)
}

// SetRecurringDetector sets the recurring expense detector.
func (o *Orchestrator) SetRecurringDetector(d RecurringExpenseDetector) {
	o.recurringDetector = d
}

// RecurringExpenseTool returns the tool definition.
func RecurringExpenseTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetRecurringExpenses,
		Description: "Analyze receipts and card transactions to detect recurring expenses (subscriptions, regular purchases). Use when user asks about recurring spending, subscriptions, or regular expenses.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *Orchestrator) executeRecurringExpenses(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.recurringDetector == nil {
		return map[string]interface{}{"error": "recurring expense detection not available"}, nil
	}
	expenses, err := o.recurringDetector.DetectRecurring(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("detect recurring: %w", err)
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

	return map[string]interface{}{
		"recurring_expenses":      items,
		"total_recurring_monthly": totalMonthly.StringFixed(2),
		"insight":                 fmt.Sprintf("You have %d recurring expenses totaling $%s/month", len(expenses), totalMonthly.StringFixed(2)),
	}, nil
}

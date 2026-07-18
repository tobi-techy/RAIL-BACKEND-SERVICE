package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolGetBudget = "get_budget"
	ToolSetBudget = "set_budget"
)

// BudgetProvider reads and writes spending budgets.
type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
	Upsert(ctx context.Context, userID uuid.UUID, limit decimal.Decimal, currency string) error
}

// SetBudgetProvider sets the budget provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetBudgetProvider(b BudgetProvider) {
	o.budgetProvider = b
}

// BudgetTools returns tool definitions for budget management.
func BudgetTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetBudget,
			Description: "Get the user's monthly spending budget and current progress. Shows budget limit, amount spent so far this month, remaining budget, and percentage used. Use when user asks about their budget, spending limit, or how much they can still spend.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolSetBudget,
			Description: "Set or update the user's monthly spending budget. Use when user says 'set my budget to X', 'I want to spend no more than X per month', or 'change my budget'.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"monthly_limit": map[string]interface{}{"type": "number", "description": "Monthly spending limit in USD"},
				},
				"required": []string{"monthly_limit"},
			},
		},
	}
}

func (o *AgentAdapter) executeGetBudget(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.budgetProvider == nil {
		return map[string]interface{}{"error": "budgets not available"}, nil
	}

	budget, err := o.budgetProvider.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get budget: %w", err)
	}
	if budget == nil {
		return map[string]interface{}{
			"has_budget": false,
			"message":    "No monthly budget set. Ask the user if they'd like to set one.",
		}, nil
	}

	// Get current month spending
	var monthlySpend decimal.Decimal
	if o.spending != nil {
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		summary, err := o.spending.GetSummary(ctx, userID, monthStart, now)
		if err == nil {
			monthlySpend = summary.Total
		}
	}

	remaining := budget.MonthlyLimit.Sub(monthlySpend)
	pctUsed := decimal.Zero
	if !budget.MonthlyLimit.IsZero() {
		pctUsed = monthlySpend.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
	}

	now := time.Now().UTC()
	daysLeft := daysRemainingInMonth(now)

	status := "on_track"
	if pctUsed.GreaterThan(decimal.NewFromInt(90)) {
		status = "almost_exceeded"
	}
	if remaining.IsNegative() {
		status = "exceeded"
	}

	return map[string]interface{}{
		"has_budget":    true,
		"monthly_limit": budget.MonthlyLimit.StringFixed(2),
		"spent_so_far":  monthlySpend.StringFixed(2),
		"remaining":     remaining.StringFixed(2),
		"percent_used":  pctUsed.StringFixed(1),
		"status":        status,
		"days_left":     daysLeft,
		"daily_allowance": func() string {
			if daysLeft > 0 && remaining.IsPositive() {
				return remaining.Div(decimal.NewFromInt(int64(daysLeft))).StringFixed(2)
			}
			return "0.00"
		}(),
	}, nil
}

func (o *AgentAdapter) createSetBudgetAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	limit, ok := args["monthly_limit"].(float64)
	if !ok || limit <= 0 {
		return map[string]interface{}{"error": "monthly_limit must be a positive number"}, nil
	}

	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSetBudget,
		Description:    fmt.Sprintf("Set monthly spending budget to $%.2f", limit),
		Params:         map[string]interface{}{"monthly_limit": limit},
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}

	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending budget action: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

func (o *AgentAdapter) executeSetBudget(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.budgetProvider == nil {
		return map[string]interface{}{"error": "budgets not available"}, nil
	}

	limit, ok := args["monthly_limit"].(float64)
	if !ok || limit <= 0 {
		return map[string]interface{}{"error": "monthly_limit must be a positive number"}, nil
	}

	err := o.budgetProvider.Upsert(ctx, userID, decimal.NewFromFloat(limit), "USD")
	if err != nil {
		return nil, fmt.Errorf("set budget: %w", err)
	}

	return map[string]interface{}{
		"success":       true,
		"monthly_limit": fmt.Sprintf("%.2f", limit),
		"message":       fmt.Sprintf("Monthly spending budget set to $%.2f. I'll track your progress.", limit),
	}, nil
}

func daysRemainingInMonth(now time.Time) int {
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return int(nextMonth.Sub(now).Hours() / 24)
}

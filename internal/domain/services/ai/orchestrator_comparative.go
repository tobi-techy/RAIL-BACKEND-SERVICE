package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	aifinance "github.com/rail-service/rail_service/internal/domain/services/ai/finance"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetComparativeContext = "get_comparative_context"

// AggregateStatsProvider returns anonymous aggregate stats for comparison.
// Deprecated: Use aifinance.AggregateStatsProvider instead.
type AggregateStatsProvider = aifinance.AggregateStatsProvider

// SetAggregateStats sets the aggregate stats provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetAggregateStats(a AggregateStatsProvider) {
	o.aggregateStats = a
}

func ComparativeContextTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetComparativeContext,
		Description: "Get the user's financial position with context: current balances, savings rate, and how they compare. Use when user asks 'how am I doing?', 'am I saving enough?', or wants perspective on their progress.",
		Parameters: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeComparativeContext(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.aggregateStats == nil {
		return map[string]interface{}{"error": "comparative data not available"}, nil
	}

	spend, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	total := spend.Add(stash)

	savingsRate := decimal.Zero
	if !total.IsZero() {
		savingsRate = stash.Div(total).Mul(decimal.NewFromInt(100))
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Streak data
	var streakDays int
	if o.activityProvider != nil {
		streak, err := o.activityProvider.GetStreak(ctx, userID)
		if err == nil && streak != nil {
			streakDays = streak.CurrentStreak
		}
	}

	// Spending this month
	var monthlySpend decimal.Decimal
	if o.spending != nil {
		summary, err := o.spending.GetSummary(ctx, userID, monthStart, now)
		if err == nil {
			monthlySpend = summary.Total
		}
	}

	// Build context narrative data
	stashLevel := "starting"
	if stash.GreaterThanOrEqual(decimal.NewFromInt(100)) {
		stashLevel = "building"
	}
	if stash.GreaterThanOrEqual(decimal.NewFromInt(500)) {
		stashLevel = "growing"
	}
	if stash.GreaterThanOrEqual(decimal.NewFromInt(1000)) {
		stashLevel = "strong"
	}
	if stash.GreaterThanOrEqual(decimal.NewFromInt(5000)) {
		stashLevel = "impressive"
	}

	return map[string]interface{}{
		"spend_balance":     spend.String(),
		"stash_balance":     stash.String(),
		"total_balance":     total.String(),
		"savings_rate":      savingsRate.StringFixed(1) + "%",
		"stash_level":       stashLevel,
		"streak_days":       streakDays,
		"monthly_spend":     monthlySpend.String(),
		"days_into_month":   now.Day(),
		"note":              "spend_balance = available to spend (70% side). stash_balance = savings earning yield (30% side). total_balance = spend + stash. monthly_spend = completed outflows this month only.",
		"context_narrative": fmt.Sprintf("Spend: $%s available. Stash: $%s (%s level). Total: $%s. Savings rate: %s%%. Streak: %d days. Monthly spend so far: $%s (%d days in).", spend.StringFixed(2), stash.StringFixed(2), stashLevel, total.StringFixed(2), savingsRate.StringFixed(1), streakDays, monthlySpend.StringFixed(2), now.Day()),
	}, nil
}

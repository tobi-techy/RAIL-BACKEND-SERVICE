package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// quickReplyPatterns defines the keyword patterns that trigger the fast path.
// These are intentionally narrower than the tool router patterns — we only
// bypass the LLM for very high-confidence, unambiguous queries.
var (
	balancePatterns = []string{
		"balance", "how much do i have", "what do i have",
		"what's my balance", "whats my balance", "my balance",
		"how much money", "how much is in my",
		"spend balance", "stash balance", "what am i working with",
	}

	spendingSummaryPatterns = []string{
		"how much did i spend", "what did i spend", "spending this month",
		"spend this month", "this month spending", "how much have i spent",
		"my spending", "spending summary",
	}

	budgetPatterns = []string{
		"budget", "how's my budget", "whats my budget", "what's my budget",
		"budget status", "am i on budget", "budget left",
	}
)

// fmtUSD formats a decimal as a USD string with 2 decimal places.
func fmtUSD(d decimal.Decimal) string {
	return "$" + d.StringFixed(2)
}

// QuickReply checks if the message matches a common financial query that can
// be answered directly from tool data without an LLM call. Returns
// (content, cards, true) if handled, or ("", nil, false) to continue the
// normal pipeline.
func (o *AgentAdapter) QuickReply(ctx context.Context, userID uuid.UUID, message string) (string, []map[string]interface{}, bool) {
	lower := strings.ToLower(strings.TrimSpace(message))

	// Only attempt fast path for short, focused queries (no multi-sentence)
	if len(lower) > 80 || strings.Count(lower, ".") > 1 {
		return "", nil, false
	}

	if matchesAny(lower, balancePatterns) && !matchesAny(lower, actionPatterns) {
		return o.quickBalanceReply(ctx, userID)
	}

	if matchesAny(lower, spendingSummaryPatterns) && !matchesAny(lower, actionPatterns) {
		return o.quickSpendingReply(ctx, userID)
	}

	if matchesAny(lower, budgetPatterns) && !matchesAny(lower, actionPatterns) {
		return o.quickBudgetReply(ctx, userID)
	}

	return "", nil, false
}

// quickBalanceReply returns a formatted balance summary without an LLM call.
func (o *AgentAdapter) quickBalanceReply(ctx context.Context, userID uuid.UUID) (string, []map[string]interface{}, bool) {
	if o.aggregateStats == nil {
		return "", nil, false
	}
	spend, spendErr := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, stashErr := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if spendErr != nil || stashErr != nil {
		return "", nil, false
	}
	total := spend.Add(stash)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You have %s in Spend and %s in Stash — %s total.", fmtUSD(spend), fmtUSD(stash), fmtUSD(total)))

	// Add budget context if we have it and spending isn't far behind
	if o.budgetProvider != nil {
		budget, err := o.budgetProvider.GetByUserID(ctx, userID)
		if err == nil && budget != nil && !budget.MonthlyLimit.IsZero() {
			if o.spending != nil {
				now := time.Now().UTC()
				monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
				summary, sErr := o.spending.GetSummary(ctx, userID, monthStart, now)
				if sErr == nil {
					remaining := budget.MonthlyLimit.Sub(summary.Total)
					pct := summary.Total.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
					if remaining.IsNegative() {
						b.WriteString(fmt.Sprintf(" You're %s over budget (%s%% used).", fmtUSD(remaining.Neg()), pct.StringFixed(0)))
					} else if pct.GreaterThan(decimal.NewFromFloat(80)) {
						b.WriteString(fmt.Sprintf(" Budget: %s left of %s (%s%% used).", fmtUSD(remaining), fmtUSD(budget.MonthlyLimit), pct.StringFixed(0)))
					}
				}
			}
		}
	}

	return b.String(), nil, true
}

// quickSpendingReply returns a formatted spending summary without an LLM call.
func (o *AgentAdapter) quickSpendingReply(ctx context.Context, userID uuid.UUID) (string, []map[string]interface{}, bool) {
	if o.spending == nil {
		return "", nil, false
	}
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	summary, err := o.spending.GetSummary(ctx, userID, monthStart, now)
	if err != nil {
		return "", nil, false
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("You've spent %s this month across %d transactions.", fmtUSD(summary.Total), summary.TxCount))
	if summary.DailyAvg.GreaterThan(decimal.Zero) {
		b.WriteString(fmt.Sprintf(" That's %s/day on average.", fmtUSD(summary.DailyAvg)))
	}
	if len(summary.Categories) > 0 && summary.Categories[0].Total.GreaterThan(decimal.Zero) {
		b.WriteString(fmt.Sprintf(" Top category: %s at %s.", summary.Categories[0].Category, fmtUSD(summary.Categories[0].Total)))
	}

	return b.String(), nil, true
}

// quickBudgetReply returns a formatted budget status without an LLM call.
func (o *AgentAdapter) quickBudgetReply(ctx context.Context, userID uuid.UUID) (string, []map[string]interface{}, bool) {
	if o.budgetProvider == nil {
		return "", nil, false
	}
	budget, err := o.budgetProvider.GetByUserID(ctx, userID)
	if err != nil || budget == nil || budget.MonthlyLimit.IsZero() {
		return "You haven't set a budget yet. Want me to set one?", nil, true
	}

	monthlySpend := decimal.Zero
	if o.spending != nil {
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		summary, sErr := o.spending.GetSummary(ctx, userID, monthStart, now)
		if sErr == nil {
			monthlySpend = summary.Total
		}
	}

	remaining := budget.MonthlyLimit.Sub(monthlySpend)
	pctUsed := monthlySpend.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Budget: %s. Spent %s (%s%%).", fmtUSD(budget.MonthlyLimit), fmtUSD(monthlySpend), pctUsed.StringFixed(0)))

	if remaining.IsNegative() {
		b.WriteString(fmt.Sprintf(" You're %s over budget.", fmtUSD(remaining.Neg())))
	} else {
		b.WriteString(fmt.Sprintf(" %s remaining.", fmtUSD(remaining)))
	}

	// Days remaining in the month
	now := time.Now().UTC()
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	daysLeft := int(nextMonth.Sub(now).Hours() / 24)
	if daysLeft > 0 && remaining.IsPositive() {
		dailyBudget := remaining.Div(decimal.NewFromInt(int64(daysLeft)))
		b.WriteString(fmt.Sprintf(" That's %s/day for the next %d days.", fmtUSD(dailyBudget), daysLeft))
	}

	return b.String(), nil, true
}

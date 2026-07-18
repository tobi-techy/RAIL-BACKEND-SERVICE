package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ConversationStarter is a contextual prompt suggestion shown to the user.
type ConversationStarter struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "spending", "saving", "insight", "action"
}

func (o *AgentAdapter) buildStarterContext(ctx context.Context, userID uuid.UUID) string {
	var parts []string

	spend, stash, total := o.currentBalances(ctx, userID)
	if total.IsPositive() {
		parts = append(parts, fmt.Sprintf("Balance: $%s (spend $%s, stash $%s)", total.StringFixed(2), spend.StringFixed(2), stash.StringFixed(2)))
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	flow := o.monthFlow(ctx, userID, monthStart, now)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
	if flow.TotalDeposits.IsPositive() || totalOut.IsPositive() {
		parts = append(parts, fmt.Sprintf("This month: $%s in, $%s out", flow.TotalDeposits.StringFixed(2), totalOut.StringFixed(2)))
	}

	if o.patterns != nil {
		if totalSpend, txCount, err := o.patterns.GetSpendingTotal(ctx, userID, monthStart, now); err == nil && txCount > 0 {
			parts = append(parts, fmt.Sprintf("Spent $%s across %d transactions this month", totalSpend.StringFixed(2), txCount))
		}
	}

	if o.activityProvider != nil {
		if streak, err := o.activityProvider.GetStreak(ctx, userID); err == nil && streak != nil && streak.CurrentStreak > 0 {
			parts = append(parts, fmt.Sprintf("Saving streak: %d days", streak.CurrentStreak))
		}
	}

	if o.memory != nil {
		if memCtx := o.memory.BuildMemoryContextWithSummary(ctx, userID, ""); memCtx != "" && len(memCtx) < 500 {
			parts = append(parts, fmt.Sprintf("Known about user: %s", memCtx))
		}
	}

	// Statement-based proactive nudges
	if o.bankStatementCtx != nil && o.bankStatementCtx.provider != nil {
		if currentSpend, dailyAvg, err := o.bankStatementCtx.provider.GetDailySpendingPace(ctx, userID); err == nil && dailyAvg > 0 {
			daysElapsed := float64(now.Day())
			if daysElapsed < 1 {
				daysElapsed = 1
			}
			currentDailyPace := currentSpend / daysElapsed
			if currentDailyPace > dailyAvg*1.2 {
				parts = append(parts, fmt.Sprintf("⚠️ Spending pace alert: %.0f/day vs usual %.0f/day", currentDailyPace, dailyAvg))
			}
		}
		if names, _, err := o.bankStatementCtx.provider.GetTopRecurringRecipients(ctx, userID, 3); err == nil && len(names) > 0 {
			parts = append(parts, fmt.Sprintf("Recurring payments to: %s", strings.Join(names, ", ")))
		}
	}

	parts = append(parts, fmt.Sprintf("Day: %s, time: %s", now.Format("Monday"), now.Format("3pm")))

	return strings.Join(parts, "\n")
}

func (o *AgentAdapter) fallbackStarters() []ConversationStarter {
	return []ConversationStarter{
		{Text: "Where did my money go this month?", Category: "spending"},
		{Text: "How is my stash doing?", Category: "saving"},
		{Text: "What are my spending patterns?", Category: "insight"},
		{Text: "Help me save more this month", Category: "action"},
	}
}

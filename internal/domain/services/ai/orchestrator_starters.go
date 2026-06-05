package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// ConversationStarter is a contextual prompt suggestion shown to the user.
type ConversationStarter struct {
	Text     string `json:"text"`
	Category string `json:"category"` // "spending", "saving", "insight", "action"
}

// GetConversationStarters returns 4 personalized conversation starters based on the user's
// actual financial state. Uses the fast model to keep latency under 2 seconds.
func (o *Orchestrator) GetConversationStarters(ctx context.Context, userID uuid.UUID) []ConversationStarter {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	snapshot := o.buildStarterContext(ctx, userID)
	if snapshot == "" {
		return o.fallbackStarters()
	}

	prompt := fmt.Sprintf(`Based on this user's financial snapshot, generate exactly 4 short conversation starters (max 10 words each) that would make them want to tap and chat. Make them specific to their numbers — not generic.

Return JSON array only, no markdown: [{"text":"...","category":"spending|saving|insight|action"}, ...]

User snapshot:
%s`, snapshot)

	resp, err := o.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		Messages:     []infraai.Message{{Role: "user", Content: prompt}},
		SystemPrompt: "You generate short, punchy conversation starters for a money app. Be specific with numbers. No emojis. Return only valid JSON.",
		MaxTokens:    300,
		Temperature:  infraai.Float64(0.8),
		ModelHint:    "fast",
	})
	if err != nil {
		o.logger.Debug("conversation starters: AI failed, using fallback", zap.Error(err))
		return o.fallbackStarters()
	}

	var starters []ConversationStarter
	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &starters); err != nil {
		o.logger.Debug("conversation starters: parse failed", zap.Error(err), zap.String("raw", content))
		return o.fallbackStarters()
	}

	if len(starters) == 0 {
		return o.fallbackStarters()
	}
	if len(starters) > 4 {
		starters = starters[:4]
	}
	return starters
}

func (o *Orchestrator) buildStarterContext(ctx context.Context, userID uuid.UUID) string {
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
		if memCtx := o.memory.BuildMemoryContextWithSummary(ctx, userID); memCtx != "" && len(memCtx) < 500 {
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

func (o *Orchestrator) fallbackStarters() []ConversationStarter {
	return []ConversationStarter{
		{Text: "Where did my money go this month?", Category: "spending"},
		{Text: "How is my stash doing?", Category: "saving"},
		{Text: "What are my spending patterns?", Category: "insight"},
		{Text: "Help me save more this month", Category: "action"},
	}
}

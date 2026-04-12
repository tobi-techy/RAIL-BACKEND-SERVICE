package ai

import (
	"context"

	"github.com/google/uuid"
)

// GetPersonalizedSuggestions returns contextual suggestions based on user data.
func (o *Orchestrator) GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string {
	suggestions := []string{"Where did my money go this month?"}

	if o.portfolioProvider != nil {
		stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
		if err == nil && stats != nil {
			if stats.WeeklyReturnPct.IsNegative() {
				suggestions = append(suggestions, "Why is my portfolio down this week?")
			} else {
				suggestions = append(suggestions, "How is my portfolio doing?")
			}
		}
	}

	if o.activityProvider != nil {
		streak, err := o.activityProvider.GetStreak(ctx, userID)
		if err == nil && streak != nil && streak.CurrentStreak > 3 {
			suggestions = append(suggestions, "How long is my investing streak?")
		} else {
			suggestions = append(suggestions, "How can I build a saving habit?")
		}
	}

	if o.balanceHistory != nil {
		suggestions = append(suggestions, "Show me how my savings have grown")
	}
	if o.knowledge != nil {
		suggestions = append(suggestions, "What's the best way to start investing?")
	}
	if o.spending != nil {
		suggestions = append(suggestions, "What are my top spending categories?")
	}

	if len(suggestions) > 6 {
		suggestions = suggestions[:6]
	}
	return suggestions
}

package ai

import (
	"context"

	"github.com/google/uuid"
)

// GetPersonalizedSuggestions returns contextual suggestions based on user data.
func (o *Orchestrator) GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string {
	suggestions := []string{"Where did my money go this month?"}
	if o.spending != nil && o.aggregateStats != nil && o.financialProfile != nil {
		suggestions = append(suggestions,
			"What's my financial health score?",
			"Forecast my end-of-month balance",
			"Build my personal money plan",
			"Check my financial risks",
			"Show my financial timeline",
		)
	}

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

	if o.patterns != nil {
		suggestions = append(suggestions, "What are my spending patterns?")
	}

	// New: simulator suggestion
	suggestions = append(suggestions, "What if I save $50 every week for a year?")

	// New: comparative
	if o.aggregateStats != nil {
		suggestions = append(suggestions, "How am I doing financially?")
	}

	if o.balanceHistory != nil {
		suggestions = append(suggestions, "Show me how my savings have grown")
	}

	if o.knowledge != nil {
		suggestions = append(suggestions, "What's the best way to start investing?")
	}

	// Feature discovery — introduce new capabilities
	suggestions = append(suggestions,
		"Set up an automation to save every Friday",
		"Create a savings goal with friends",
	)

	if len(suggestions) > 8 {
		suggestions = suggestions[:8]
	}
	return suggestions
}

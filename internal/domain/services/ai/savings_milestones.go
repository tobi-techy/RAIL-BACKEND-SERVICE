package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Milestone thresholds in USD.
var milestoneThresholds = []decimal.Decimal{
	decimal.NewFromInt(100),
	decimal.NewFromInt(250),
	decimal.NewFromInt(500),
	decimal.NewFromInt(1000),
	decimal.NewFromInt(2500),
	decimal.NewFromInt(5000),
	decimal.NewFromInt(10000),
}

// MilestoneAlert represents a savings milestone the user just crossed.
type MilestoneAlert struct {
	UserID    uuid.UUID `json:"user_id"`
	Milestone string    `json:"milestone"`
	Balance   string    `json:"balance"`
	Message   string    `json:"message"`
}

// CheckSavingsMilestone checks if the user just crossed a stash milestone.
// Call this after a deposit is split (stash balance increased).
// prevBalance is the stash balance before the deposit.
func (o *Orchestrator) CheckSavingsMilestone(ctx context.Context, userID uuid.UUID, prevBalance decimal.Decimal) *MilestoneAlert {
	if o.aggregateStats == nil {
		return nil
	}

	currentBalance, err := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil
	}

	for _, threshold := range milestoneThresholds {
		if prevBalance.LessThan(threshold) && currentBalance.GreaterThanOrEqual(threshold) {
			return &MilestoneAlert{
				UserID:    userID,
				Milestone: fmt.Sprintf("$%s", threshold.String()),
				Balance:   currentBalance.StringFixed(2),
				Message:   milestoneMessage(threshold, currentBalance),
			}
		}
	}
	return nil
}

func milestoneMessage(threshold, balance decimal.Decimal) string {
	switch {
	case threshold.Equal(decimal.NewFromInt(100)):
		return fmt.Sprintf("You just crossed $100 in stash! 🎉 Your savings journey is officially underway. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(250)):
		return fmt.Sprintf("$250 in stash! 💪 You're building real momentum. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(500)):
		return fmt.Sprintf("Half a thousand dollars saved! 🚀 $500 in stash and growing. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(1000)):
		return fmt.Sprintf("$1,000 in stash! 🏆 This is a huge milestone — you're in the top tier of savers. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(2500)):
		return fmt.Sprintf("$2,500 saved! 🌟 Your money is seriously working for you now. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(5000)):
		return fmt.Sprintf("$5,000 in stash! 💎 That's impressive discipline. Your yield is compounding beautifully. Balance: $%s", balance.StringFixed(2))
	case threshold.Equal(decimal.NewFromInt(10000)):
		return fmt.Sprintf("$10,000 saved! 👑 You've built something real. Balance: $%s", balance.StringFixed(2))
	default:
		return fmt.Sprintf("New savings milestone: $%s! Balance: $%s 🎉", threshold.String(), balance.StringFixed(2))
	}
}

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

// DailyBriefing is a pre-computed spoken summary for push notification trigger.
type DailyBriefing struct {
	UserID  uuid.UUID `json:"user_id"`
	Summary string    `json:"summary"` // Plain spoken text, ready for TTS or voice greeting
	Date    time.Time `json:"date"`
}

// GenerateDailyBriefing builds a 30-second spoken summary of the user's financial state.
// Called by a scheduled worker, result stored for push notification trigger.
func (o *Orchestrator) GenerateDailyBriefing(ctx context.Context, userID uuid.UUID) (*DailyBriefing, error) {
	var parts []string
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)

	name := o.realtimeFirstName(ctx, userID)
	if name != "" {
		parts = append(parts, fmt.Sprintf("Morning %s.", name))
	} else {
		parts = append(parts, "Morning.")
	}

	// Balances
	if o.aggregateStats != nil {
		spend, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		stash, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
		if !spend.IsZero() || !stash.IsZero() {
			parts = append(parts, fmt.Sprintf("Spend is %s, stash is %s.", spokenAmount(spend), spokenAmount(stash)))
		}
	}

	// Yesterday's deposits
	if o.depositHistory != nil {
		deposits, err := o.depositHistory.GetByUserID(ctx, userID, 10, 0)
		if err == nil {
			var dayDeposits decimal.Decimal
			for _, d := range deposits {
				if d.CreatedAt.After(yesterday) {
					dayDeposits = dayDeposits.Add(d.Amount)
				}
			}
			if !dayDeposits.IsZero() {
				parts = append(parts, fmt.Sprintf("You received %s yesterday.", spokenAmount(dayDeposits)))
			}
		}
	}

	// Yesterday's spending
	if o.spending != nil {
		summary, err := o.spending.GetSummary(ctx, userID, yesterday, now)
		if err == nil && summary != nil && !summary.Total.IsZero() {
			parts = append(parts, fmt.Sprintf("You spent %s yesterday.", spokenAmount(summary.Total)))
		}
	}

	// Yield earned (last 7 days)
	if o.yieldProvider != nil {
		weekAgo := now.AddDate(0, 0, -7)
		snapshots, err := o.yieldProvider.GetSnapshotsInWindow(ctx, userID, weekAgo, now)
		if err == nil && len(snapshots) >= 2 {
			first := snapshots[0].Balance
			last := snapshots[len(snapshots)-1].Balance
			earned := last.Sub(first)
			if earned.GreaterThan(decimal.Zero) {
				parts = append(parts, fmt.Sprintf("Stash earned %s in yield this week.", spokenAmount(earned)))
			}
		}
	}

	// Upcoming obligations
	if o.obligations != nil {
		all, err := o.obligations.ListActive(ctx, userID)
		if err == nil {
			for _, ob := range all {
				if ob.DueDate == nil {
					continue
				}
				daysUntil := int(time.Until(*ob.DueDate).Hours() / 24)
				if daysUntil >= 0 && daysUntil <= 3 {
					parts = append(parts, fmt.Sprintf("%s is due in %d days.", ob.Name, daysUntil))
					break
				}
			}
		}
	}

	// Failed withdrawals
	if o.withdrawalHistory != nil {
		withdrawals, _ := o.withdrawalHistory.GetByUserID(ctx, userID, 3, 0)
		for _, w := range withdrawals {
			if w.Status == entities.WithdrawalStatusFailed && w.CreatedAt.After(yesterday) {
				parts = append(parts, fmt.Sprintf("Heads up — a %s %s withdrawal failed.", w.Amount.StringFixed(0), string(w.Currency)))
				break
			}
		}
	}

	if len(parts) <= 1 {
		parts = append(parts, "All quiet on the money front. Have a good day.")
	} else {
		parts = append(parts, "That's your brief.")
	}

	return &DailyBriefing{
		UserID:  userID,
		Summary: strings.Join(parts, " "),
		Date:    now,
	}, nil
}

func spokenAmount(d decimal.Decimal) string {
	val := d.IntPart()
	if val >= 1000 {
		return fmt.Sprintf("about %d dollars", val)
	}
	if val == 0 {
		cents := d.Mul(decimal.NewFromInt(100)).IntPart()
		if cents > 0 {
			return fmt.Sprintf("%d cents", cents)
		}
		return "zero"
	}
	return fmt.Sprintf("%d dollars", val)
}

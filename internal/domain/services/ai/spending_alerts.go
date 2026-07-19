package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SpendingAlert represents a real-time spending alert for the user.
type SpendingAlert struct {
	UserID  uuid.UUID `json:"user_id"`
	Type    string    `json:"type"`    // "high_spend", "budget_warning", "unusual_merchant"
	Message string    `json:"message"` // Friendly alert text
	Amount  string    `json:"amount"`
}

// CheckSpendingAlert analyzes a new transaction and returns alerts if anomalous.
// Called from card/withdrawal webhook handlers after a transaction completes.
func (o *AgentAdapter) CheckSpendingAlert(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, merchant, category string) []*SpendingAlert {
	if o.spending == nil {
		return nil
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Get this month's spending so far
	summary, err := o.spending.GetSummary(ctx, userID, monthStart, now)
	if err != nil || summary.TxCount < 3 {
		return nil // Not enough data to detect anomalies
	}

	dailyAvg := summary.DailyAvg
	alerts := make([]*SpendingAlert, 0)

	// Look up enrichment context for the merchant
	merchantContext := ""
	if enrichmentMap := enrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		if et := lookupEnrichment(merchant, enrichmentMap); et != nil {
			merchantContext = et.MerchantContext
		}
	}

	// Alert 1: Single transaction > 3x daily average
	if !dailyAvg.IsZero() && amount.GreaterThan(dailyAvg.Mul(decimal.NewFromInt(3))) {
		msg := fmt.Sprintf("Heads up — that $%s transaction is more than 3x your daily average of $%s", amount.StringFixed(2), dailyAvg.StringFixed(2))
		if merchantContext != "" {
			msg += fmt.Sprintf(" (%s)", merchantContext)
		}
		alerts = append(alerts, &SpendingAlert{
			UserID:  userID,
			Type:    "high_spend",
			Message: msg,
			Amount:  amount.String(),
		})
	}

	// Alert 2: Budget warning
	if o.budgetProvider != nil {
		budget, err := o.budgetProvider.GetByUserID(ctx, userID)
		if err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			spent := summary.Total.Add(amount)
			pct := spent.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
			remaining := budget.MonthlyLimit.Sub(spent)

			if remaining.IsNegative() {
				alerts = append(alerts, &SpendingAlert{
					UserID:  userID,
					Type:    "budget_exceeded",
					Message: fmt.Sprintf("You've exceeded your $%s monthly budget by $%s", budget.MonthlyLimit.StringFixed(2), remaining.Abs().StringFixed(2)),
					Amount:  amount.String(),
				})
			} else if pct.GreaterThan(decimal.NewFromInt(80)) {
				alerts = append(alerts, &SpendingAlert{
					UserID:  userID,
					Type:    "budget_warning",
					Message: fmt.Sprintf("You've used %s%% of your $%s monthly budget. $%s remaining.", pct.StringFixed(0), budget.MonthlyLimit.StringFixed(2), remaining.StringFixed(2)),
					Amount:  amount.String(),
				})
			}
		}
	}

	return alerts
}

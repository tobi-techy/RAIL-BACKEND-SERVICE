package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BankStatementContextProvider supplies external bank statement data.
type BankStatementContextProvider struct {
	BuildFn func(ctx context.Context, userID uuid.UUID) string
}

// NewBankStatementContextProvider creates a new provider.
func NewBankStatementContextProvider(buildFn func(ctx context.Context, userID uuid.UUID) string) *BankStatementContextProvider {
	if buildFn == nil {
		return nil
	}
	return &BankStatementContextProvider{BuildFn: buildFn}
}

// BuildContext returns a system-level context string summarizing the user's
// external bank statement data.
func (p *BankStatementContextProvider) BuildContext(ctx context.Context, userID uuid.UUID) string {
	if p == nil || p.BuildFn == nil {
		return ""
	}
	return p.BuildFn(ctx, userID)
}

// BuildBankStatementContext is the standalone function that does the actual
// bank statement context building. This is passed as a function to the
// context package to avoid import cycles.
func BuildBankStatementContext(
	ctx context.Context,
	userID uuid.UUID,
	getSpendingSummary func(ctx context.Context, userID uuid.UUID, start, end time.Time) (map[string]float64, error),
	getUploadSummary func(ctx context.Context, userID uuid.UUID) (int, []string, error),
	getRecurringRecipients func(ctx context.Context, userID uuid.UUID, limit int) ([]string, []int, error),
	getDailyPace func(ctx context.Context, userID uuid.UUID) (float64, float64, error),
) string {
	if getSpendingSummary == nil || getUploadSummary == nil {
		return ""
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	now := time.Now().UTC()
	sixMonthsAgo := now.AddDate(0, -6, 0)

	summary, err := getSpendingSummary(fetchCtx, userID, sixMonthsAgo, now)
	if err != nil || len(summary) == 0 {
		return ""
	}

	totalTxns, banks, err := getUploadSummary(fetchCtx, userID)
	if err != nil || len(banks) == 0 {
		return ""
	}

	var parts []string
	var totalSpend float64
	var topCat string
	var topCatAmount float64
	for cat, amount := range summary {
		parts = append(parts, fmt.Sprintf("%s: %.0f", cat, amount))
		totalSpend += amount
		if amount > topCatAmount {
			topCat = cat
			topCatAmount = amount
		}
	}

	result := fmt.Sprintf(
		"[External bank data — %d transactions from %s. Spending by category (last 6 months): %s. Total external spend: %.0f.",
		totalTxns,
		strings.Join(banks, ", "),
		strings.Join(parts, " | "),
		totalSpend,
	)

	if topCat != "" {
		monthlyAvg := topCatAmount / 6
		result += fmt.Sprintf(" Highest category: %s (%.0f/month avg).", topCat, monthlyAvg)
	}

	if getRecurringRecipients != nil {
		if names, counts, err := getRecurringRecipients(fetchCtx, userID, 5); err == nil && len(names) > 0 {
			var recips []string
			for i, name := range names {
				recips = append(recips, fmt.Sprintf("%s (%dx)", name, counts[i]))
			}
			result += fmt.Sprintf(" Recurring recipients: %s.", strings.Join(recips, ", "))
		}
	}

	if getDailyPace != nil {
		if currentSpend, dailyAvg, err := getDailyPace(fetchCtx, userID); err == nil && dailyAvg > 0 {
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			daysElapsed := now.Sub(monthStart).Hours() / 24
			if daysElapsed < 1 {
				daysElapsed = 1
			}
			currentDailyPace := currentSpend / daysElapsed
			result += fmt.Sprintf(" Current daily pace: %.0f vs historical avg: %.0f/day.", currentDailyPace, dailyAvg)
		}
	}

	result += " Use this data when the user asks about spending patterns, budgets, or financial habits outside Rail.]"
	return result
}

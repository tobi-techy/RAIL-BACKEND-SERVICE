package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BankStatementSummaryProvider abstracts the bank statement repository to avoid import cycles.
type BankStatementSummaryProvider interface {
	GetSpendingSummaryByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) (map[string]float64, error)
	GetCompletedUploadSummary(ctx context.Context, userID uuid.UUID) (totalTxns int, banks []string, err error)
	GetTopRecurringRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]string, []int, error)
	GetDailySpendingPace(ctx context.Context, userID uuid.UUID) (currentMonthSpend float64, historicalDailyAvg float64, err error)
	GetIncomeExpenseSummary(ctx context.Context, userID uuid.UUID) (totalIncome, totalExpense decimal.Decimal, periodStart, periodEnd *time.Time, err error)
	GetCategoryMonthlyAverages(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error)
}

// BankStatementContextProvider supplies external bank statement data to the orchestrator.
type BankStatementContextProvider struct {
	provider BankStatementSummaryProvider
}

// SetBankStatementContext wires the bank statement context provider into the orchestrator.
func (o *AgentAdapter) SetBankStatementContext(p *BankStatementContextProvider) {
	o.bankStatementCtx = p
}

// SetMonoAnalysis wires the Mono spending analysis provider into the orchestrator.
// When set, Miriam can access spending data from Mono-linked bank accounts in
// addition to (or instead of) uploaded bank statements.
func (o *AgentAdapter) SetMonoAnalysis(p MonoAnalysisProvider) {
	o.monoAnalysis = p
}

func NewBankStatementContextProvider(provider BankStatementSummaryProvider) *BankStatementContextProvider {
	if provider == nil {
		return nil
	}
	return &BankStatementContextProvider{provider: provider}
}

// BuildContext returns a system-level context string summarizing the user's
// external bank statement data for Miriam to reference in chat.
func (p *BankStatementContextProvider) BuildContext(ctx context.Context, userID uuid.UUID) string {
	if p == nil || p.provider == nil {
		return ""
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Get spending summary for last 6 months
	now := time.Now().UTC()
	sixMonthsAgo := now.AddDate(0, -6, 0)

	summary, err := p.provider.GetSpendingSummaryByCategory(fetchCtx, userID, sixMonthsAgo, now)
	if err != nil || len(summary) == 0 {
		return ""
	}

	totalTxns, banks, err := p.provider.GetCompletedUploadSummary(fetchCtx, userID)
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

	// Top spending category with monthly average
	if topCat != "" {
		monthlyAvg := topCatAmount / 6
		result += fmt.Sprintf(" Highest category: %s (%.0f/month avg).", topCat, monthlyAvg)
	}

	// Top recurring recipients
	if names, counts, err := p.provider.GetTopRecurringRecipients(fetchCtx, userID, 5); err == nil && len(names) > 0 {
		var recips []string
		for i, name := range names {
			recips = append(recips, fmt.Sprintf("%s (%dx)", name, counts[i]))
		}
		result += fmt.Sprintf(" Recurring recipients: %s.", strings.Join(recips, ", "))
	}

	// Spending pace pattern
	if currentSpend, dailyAvg, err := p.provider.GetDailySpendingPace(fetchCtx, userID); err == nil && dailyAvg > 0 {
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		daysElapsed := now.Sub(monthStart).Hours() / 24
		if daysElapsed < 1 {
			daysElapsed = 1
		}
		currentDailyPace := currentSpend / daysElapsed
		result += fmt.Sprintf(" Current daily pace: %.0f vs historical avg: %.0f/day.", currentDailyPace, dailyAvg)
	}

	result += " Use this data when the user asks about spending patterns, budgets, or financial habits outside Rail.]"
	return result
}

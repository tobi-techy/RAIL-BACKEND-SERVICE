package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BankStatementSummaryProvider abstracts the bank statement repository to avoid import cycles.
type BankStatementSummaryProvider interface {
	GetSpendingSummaryByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) (map[string]float64, error)
	GetCompletedUploadSummary(ctx context.Context, userID uuid.UUID) (totalTxns int, banks []string, err error)
}

// BankStatementContextProvider supplies external bank statement data to the orchestrator.
type BankStatementContextProvider struct {
	provider BankStatementSummaryProvider
}

// SetBankStatementContext wires the bank statement context provider into the orchestrator.
func (o *Orchestrator) SetBankStatementContext(p *BankStatementContextProvider) {
	o.bankStatementCtx = p
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
	for cat, amount := range summary {
		parts = append(parts, fmt.Sprintf("%s: %.0f", cat, amount))
		totalSpend += amount
	}

	return fmt.Sprintf(
		"[External bank data — %d transactions from %s. Spending by category (last 6 months): %s. Total external spend: %.0f. Use this data when the user asks about spending patterns, budgets, or financial habits outside Rail.]",
		totalTxns,
		strings.Join(banks, ", "),
		strings.Join(parts, " | "),
		totalSpend,
	)
}

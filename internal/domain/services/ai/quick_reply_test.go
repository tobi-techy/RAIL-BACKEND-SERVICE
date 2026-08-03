package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mocks ---

type mockAggregateStats struct {
	spendBalance decimal.Decimal
	stashBalance decimal.Decimal
	err          error
}

func (m *mockAggregateStats) GetAccountBalance(_ context.Context, _ uuid.UUID, acct entities.AccountType) (decimal.Decimal, error) {
	if m.err != nil {
		return decimal.Zero, m.err
	}
	switch acct {
	case entities.AccountTypeSpendingBalance:
		return m.spendBalance, nil
	case entities.AccountTypeStashBalance:
		return m.stashBalance, nil
	default:
		return decimal.Zero, nil
	}
}

type mockSpendingAnalyzer struct {
	summary *spending.Summary
	err     error
}

func (m *mockSpendingAnalyzer) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spending.Summary, error) {
	return m.summary, m.err
}

func (m *mockSpendingAnalyzer) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return nil, nil
}

func (m *mockSpendingAnalyzer) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return nil, nil
}

func (m *mockSpendingAnalyzer) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

type mockBudgetProvider struct {
	budget *entities.SpendingBudget
	err    error
}

func (m *mockBudgetProvider) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.SpendingBudget, error) {
	return m.budget, m.err
}

func (m *mockBudgetProvider) Upsert(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	return nil
}

// --- Quick reply pattern tests ---

func TestQuickReply_BalanceQuery(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{
		spendBalance: decimal.NewFromFloat(500),
		stashBalance: decimal.NewFromFloat(1200),
	}

	content, _, ok := o.QuickReply(context.Background(), uuid.New(), "what's my balance?")
	require.True(t, ok)
	assert.Contains(t, content, "$500.00")
	assert.Contains(t, content, "$1200.00")
	assert.Contains(t, content, "$1700.00")
}

func TestQuickReply_BalanceQueryVariant(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{
		spendBalance: decimal.NewFromFloat(42),
		stashBalance: decimal.NewFromFloat(0),
	}

	content, _, ok := o.QuickReply(context.Background(), uuid.New(), "how much do i have")
	require.True(t, ok)
	assert.Contains(t, content, "$42.00")
}

func TestQuickReply_SpendingQuery(t *testing.T) {
	o := newTestAdapter(t)
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{
			Total:    decimal.NewFromFloat(350),
			TxCount:  12,
			DailyAvg: decimal.NewFromFloat(11.67),
			Categories: []entities.SpendingByCategory{
				{Category: "Food & Drink", Total: decimal.NewFromFloat(120), Count: 5},
			},
		},
	}

	content, _, ok := o.QuickReply(context.Background(), uuid.New(), "how much did i spend this month?")
	require.True(t, ok)
	assert.Contains(t, content, "$350.00")
	assert.Contains(t, content, "12 transactions")
	assert.Contains(t, content, "$11.67")
	assert.Contains(t, content, "Food & Drink")
}

func TestQuickReply_BudgetQuery(t *testing.T) {
	o := newTestAdapter(t)
	o.budgetProvider = &mockBudgetProvider{
		budget: &entities.SpendingBudget{
			MonthlyLimit: decimal.NewFromFloat(1000),
		},
	}
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{
			Total:   decimal.NewFromFloat(650),
			TxCount: 10,
		},
	}

	content, _, ok := o.QuickReply(context.Background(), uuid.New(), "how's my budget?")
	require.True(t, ok)
	assert.Contains(t, content, "$1000.00")
	assert.Contains(t, content, "$650.00")
	assert.Contains(t, content, "65%")
	assert.Contains(t, content, "$350.00 remaining")
}

func TestQuickReply_BudgetNoBudget(t *testing.T) {
	o := newTestAdapter(t)
	o.budgetProvider = &mockBudgetProvider{budget: nil}

	content, _, ok := o.QuickReply(context.Background(), uuid.New(), "budget status")
	require.True(t, ok)
	assert.Contains(t, content, "haven't set a budget")
}

func TestQuickReply_DoesNotMatchActionIntents(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{
		spendBalance: decimal.NewFromFloat(500),
	}

	// Even though "balance" is in the message, the action intent should win
	_, _, ok := o.QuickReply(context.Background(), uuid.New(), "move $50 from spend to stash")
	assert.False(t, ok, "action intents should not trigger quick reply")
}

func TestQuickReply_DoesNotMatchComplexMessages(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{
		spendBalance: decimal.NewFromFloat(500),
	}

	_, _, ok := o.QuickReply(context.Background(), uuid.New(), "can you check my balance and then tell me if I should move some money to stash because I think I'm spending too much")
	assert.False(t, ok, "long complex messages should go through normal pipeline")
}

func TestQuickReply_NoProviderReturnsFalse(t *testing.T) {
	o := newTestAdapter(t)
	// No aggregateStats wired
	_, _, ok := o.QuickReply(context.Background(), uuid.New(), "what's my balance?")
	assert.False(t, ok)
}

func TestQuickReply_UnrecognizedMessage(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(100)}

	_, _, ok := o.QuickReply(context.Background(), uuid.New(), "what's the meaning of life?")
	assert.False(t, ok)
}

func TestQuickReply_ProviderErrorReturnsFalse(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{err: context.DeadlineExceeded}

	_, _, ok := o.QuickReply(context.Background(), uuid.New(), "balance?")
	assert.False(t, ok, "provider errors should fall through to normal pipeline")
}

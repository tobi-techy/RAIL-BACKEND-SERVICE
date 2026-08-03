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

type mockFinancialObligationProvider struct {
	obligations []entities.FinancialObligation
	err         error
}

func (m *mockFinancialObligationProvider) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	return m.obligations, m.err
}

type mockRecurringDetector struct {
	recurring []RecurringExpense
	err       error
}

func (m *mockRecurringDetector) DetectRecurring(_ context.Context, _ uuid.UUID) ([]RecurringExpense, error) {
	return m.recurring, m.err
}

type mockActivityProvider struct {
	streak *entities.InvestmentStreak
	err    error
}

func (m *mockActivityProvider) GetContributions(context.Context, uuid.UUID, string, time.Time, time.Time) (*ContributionSummary, error) {
	return nil, nil
}

func (m *mockActivityProvider) GetStreak(_ context.Context, _ uuid.UUID) (*entities.InvestmentStreak, error) {
	return m.streak, m.err
}

func TestPredictiveConversationStarters_OverBudgetRanksFirst(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(100), stashBalance: decimal.NewFromFloat(500)}
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{
			Total:   decimal.NewFromFloat(1200),
			TxCount: 20,
		},
	}
	o.budgetProvider = &mockBudgetProvider{
		budget: &entities.SpendingBudget{MonthlyLimit: decimal.NewFromFloat(1000)},
	}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Equal(t, "spending", starters[0].Category)
	assert.Contains(t, starters[0].Text, "over your $1000.00 budget")
}

func TestPredictiveConversationStarters_IdleCashSuggestion(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(600), stashBalance: decimal.NewFromFloat(0)}
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{Total: decimal.NewFromFloat(100), TxCount: 5},
	}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Equal(t, "saving", starters[0].Category)
	assert.Contains(t, starters[0].Text, "sitting in Spend")
}

func TestPredictiveConversationStarters_BillDueSoon(t *testing.T) {
	o := newTestAdapter(t)
	// Use an explicit due date so the test is stable on month boundaries (a
	// computed "today+3" day would break on the 31st).
	due := time.Now().UTC().AddDate(0, 0, 3)
	o.obligations = &mockFinancialObligationProvider{
		obligations: []entities.FinancialObligation{
			{Name: "Electricity", Amount: decimal.NewFromFloat(45), DueDate: &due},
		},
	}
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(300), stashBalance: decimal.NewFromFloat(100)}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Equal(t, "action", starters[0].Category)
	assert.Contains(t, starters[0].Text, "Electricity")
}

func TestPredictiveConversationStarters_RecurringBillTracked(t *testing.T) {
	o := newTestAdapter(t)
	o.recurringDetector = &mockRecurringDetector{
		recurring: []RecurringExpense{
			{Merchant: "Netflix", AvgAmount: decimal.NewFromFloat(12), Count: 3},
		},
	}
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(60), stashBalance: decimal.NewFromFloat(0)}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Equal(t, "insight", starters[0].Category)
	assert.Contains(t, starters[0].Text, "Netflix")
}

func TestPredictiveConversationStarters_LowSpendToppingFromStash(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(50), stashBalance: decimal.NewFromFloat(800)}
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{Total: decimal.NewFromFloat(30), TxCount: 2},
	}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Equal(t, "action", starters[0].Category)
	assert.Contains(t, starters[0].Text, "top up from Stash")
}

func TestPredictiveConversationStarters_NoProvidersDegradesToTemplates(t *testing.T) {
	o := newTestAdapter(t)

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.Len(t, starters, 4)
	assert.Equal(t, "Where did my money go this month?", starters[0].Text)
}

func TestPredictiveConversationStarters_CappedAtSix(t *testing.T) {
	o := newTestAdapter(t)
	o.aggregateStats = &mockAggregateStats{spendBalance: decimal.NewFromFloat(500), stashBalance: decimal.NewFromFloat(1200)}
	o.spending = &mockSpendingAnalyzer{
		summary: &spending.Summary{Total: decimal.NewFromFloat(400), TxCount: 30},
	}
	o.activityProvider = &mockActivityProvider{streak: &entities.InvestmentStreak{CurrentStreak: 10}}

	starters := o.PredictiveConversationStarters(context.Background(), uuid.New())
	require.NotEmpty(t, starters)
	assert.LessOrEqual(t, len(starters), 6)
}

func TestDaysUntilDue(t *testing.T) {
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)

	// Explicit due date in the future.
	future := now.AddDate(0, 0, 3)
	days, ok := daysUntilDue(nil, &future, now)
	require.True(t, ok)
	assert.Equal(t, 3, days)

	// Explicit due date later today -> 0 days, still due.
	todayLate := time.Date(2026, 8, 15, 23, 30, 0, 0, time.UTC)
	days, ok = daysUntilDue(nil, &todayLate, now)
	require.True(t, ok)
	assert.Equal(t, 0, days)

	// Due day this month.
	day := 18
	days, ok = daysUntilDue(&day, nil, now)
	require.True(t, ok)
	assert.Equal(t, 3, days)

	// Due day matches today -> 0 days, not rolled into next month.
	today := now.Day()
	days, ok = daysUntilDue(&today, nil, now)
	require.True(t, ok)
	assert.Equal(t, 0, days)

	// Due day already passed this month -> rolls into next month.
	past := 5
	days, ok = daysUntilDue(&past, nil, now)
	require.True(t, ok)
	assert.GreaterOrEqual(t, days, 17)

	// Neither set -> undetermined.
	_, ok = daysUntilDue(nil, nil, now)
	assert.False(t, ok)
}

func TestDaysUntilDue_ClampsToNextMonthEnd(t *testing.T) {
	// Aug 15, dueDay 31 -> next occurrence is Aug 31 (this month still).
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	day := 31
	days, ok := daysUntilDue(&day, nil, now)
	require.True(t, ok)
	assert.Equal(t, 16, days)

	// Sep 15, dueDay 31 -> September has no 31st, so it clamps to Sep 30.
	now = time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	days, ok = daysUntilDue(&day, nil, now)
	require.True(t, ok)
	assert.Equal(t, 15, days)

	// Feb 15, dueDay 31 -> Feb has no 31st, clamps to Feb 28 (2026, non-leap).
	now = time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	days, ok = daysUntilDue(&day, nil, now)
	require.True(t, ok)
	assert.Equal(t, 13, days)

	// Leap year: Feb 15 2028, dueDay 31 -> clamps to Feb 29.
	now = time.Date(2028, 2, 15, 10, 0, 0, 0, time.UTC)
	days, ok = daysUntilDue(&day, nil, now)
	require.True(t, ok)
	assert.Equal(t, 14, days)

	// December year boundary: dueDay 15 already passed on Dec 20 -> rolls to
	// Jan 15 next year without producing an invalid month.
	now = time.Date(2026, 12, 20, 10, 0, 0, 0, time.UTC)
	passedDec := 15
	days, ok = daysUntilDue(&passedDec, nil, now)
	require.True(t, ok)
	assert.Equal(t, 26, days)
}

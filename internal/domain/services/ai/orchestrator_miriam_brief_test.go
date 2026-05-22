package ai

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type miriamBriefSpendingFake struct{}

func (f miriamBriefSpendingFake) GetSummary(_ context.Context, _ uuid.UUID, start, end time.Time) (*spendingsvc.Summary, error) {
	flow, _ := f.GetMoneyFlow(context.Background(), uuid.Nil, start, end)
	total := totalOutflow(flow)
	days := int(end.Sub(start).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	return &spendingsvc.Summary{Total: total, TxCount: flow.CardSpendCount + flow.WithdrawalCount + flow.P2PCount, PeriodDays: days, DailyAvg: total.Div(decimalRequireInt(days))}, nil
}

func (f miriamBriefSpendingFake) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return nil, nil
}

func (f miriamBriefSpendingFake) GetMoneyFlow(_ context.Context, _ uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	loc, _ := time.LoadLocation("Africa/Lagos")
	now := time.Now().In(loc)
	weekAgo := now.AddDate(0, 0, -7)
	prevWeekStart := now.AddDate(0, 0, -14)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	lastMonthStart := monthStart.AddDate(0, -1, 0)

	switch {
	case sameDayIn(start, weekAgo, loc):
		return &entities.MoneyFlowSummary{TotalCardSpend: decimalRequire("210"), CardSpendCount: 7}, nil
	case sameDayIn(start, prevWeekStart, loc):
		return &entities.MoneyFlowSummary{TotalCardSpend: decimalRequire("90"), CardSpendCount: 5}, nil
	case sameDayIn(start, monthStart, loc):
		return &entities.MoneyFlowSummary{TotalDeposits: decimalRequire("500"), DepositCount: 1, TotalCardSpend: decimalRequire("420"), CardSpendCount: 12}, nil
	case sameDayIn(start, lastMonthStart, loc):
		return &entities.MoneyFlowSummary{TotalDeposits: decimalRequire("600"), DepositCount: 2, TotalCardSpend: decimalRequire("250"), CardSpendCount: 9}, nil
	default:
		return &entities.MoneyFlowSummary{}, nil
	}
}

func (f miriamBriefSpendingFake) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

func TestExecuteMiriamBriefRanksChangeAndNextAction(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{spend: decimalRequire("120"), stash: decimalRequire("10")},
		spending:       miriamBriefSpendingFake{},
		budgetProvider: govBudgetProviderFake{budget: &entities.SpendingBudget{
			UserID:       userID,
			MonthlyLimit: decimalRequire("450"),
			Currency:     "USD",
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeMiriamBrief(context.Background(), userID, map[string]interface{}{"country": "NG"})
	require.NoError(t, err)
	require.Equal(t, "Africa/Lagos", result["timezone"])
	require.Equal(t, "NG", result["country"])

	snapshot := result["snapshot"].(map[string]interface{})
	require.Equal(t, "120.00", snapshot["spend_balance"])
	require.Equal(t, "420.00", snapshot["money_out"])

	insights := result["insights"].([]miriamInsight)
	require.NotEmpty(t, insights)
	require.Equal(t, "spend-runway-low", insights[0].ID)

	actions := result["next_actions"].([]miriamAction)
	require.NotEmpty(t, actions)
	require.Equal(t, "recovery_plan", actions[0].Type)
}

func TestExecuteMiriamBriefReturnsEmptyState(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{},
		spending:       operatingPlanSpendingFake{flow: &entities.MoneyFlowSummary{}},
		logger:         zap.NewNop(),
	}

	result, err := o.executeMiriamBrief(context.Background(), userID, nil)
	require.NoError(t, err)

	insights := result["insights"].([]miriamInsight)
	require.Len(t, insights, 1)
	require.Equal(t, "no-money-data-yet", insights[0].ID)
}

func sameDayIn(a, b time.Time, loc *time.Location) bool {
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}

func decimalRequireInt(v int) decimal.Decimal {
	return decimalRequire(fmt.Sprintf("%d", v))
}

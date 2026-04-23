package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type govAggregateStatsFake struct {
	spend decimal.Decimal
	stash decimal.Decimal
}

func (f govAggregateStatsFake) GetAccountBalance(_ context.Context, _ uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	switch accountType {
	case entities.AccountTypeSpendingBalance:
		return f.spend, nil
	case entities.AccountTypeStashBalance:
		return f.stash, nil
	default:
		return decimal.Zero, nil
	}
}

type govFinancialProfileFake struct {
	profile *entities.FinancialProfile
}

func (f govFinancialProfileFake) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.FinancialProfile, error) {
	return f.profile, nil
}

func (f govFinancialProfileFake) Upsert(_ context.Context, _ uuid.UUID, _ entities.FinancialProfileUpdate) (*entities.FinancialProfile, error) {
	return f.profile, nil
}

type govSavingsGoalStoreFake struct {
	goal *SavingsGoal
}

func (f govSavingsGoalStoreFake) Set(_ context.Context, _ uuid.UUID, _ *SavingsGoal) error {
	return nil
}
func (f govSavingsGoalStoreFake) Get(_ context.Context, _ uuid.UUID) (*SavingsGoal, error) {
	return f.goal, nil
}

type govBudgetProviderFake struct {
	budget *entities.SpendingBudget
}

func (f govBudgetProviderFake) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.SpendingBudget, error) {
	return f.budget, nil
}

func (f govBudgetProviderFake) Upsert(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	return nil
}

type govActionHistoryFake struct {
	actions []*entities.ActionAuditEntry
}

func (f govActionHistoryFake) ListRecentActions(_ context.Context, _ uuid.UUID, _ int) ([]*entities.ActionAuditEntry, error) {
	return f.actions, nil
}

type govDepositHistoryFake struct {
	deposits []*entities.Deposit
}

func (f govDepositHistoryFake) GetByUserID(_ context.Context, _ uuid.UUID, _ int, _ int) ([]*entities.Deposit, error) {
	return f.deposits, nil
}

type govWithdrawalHistoryFake struct {
	withdrawals []*entities.Withdrawal
}

func (f govWithdrawalHistoryFake) GetByUserID(_ context.Context, _ uuid.UUID, _ int, _ int) ([]*entities.Withdrawal, error) {
	return f.withdrawals, nil
}

func TestExecuteFinancialAdviceReturnsDeterministicChecks(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{
			spend: decimal.NewFromInt(20),
			stash: decimal.NewFromInt(10),
		},
		financialProfile: govFinancialProfileFake{
			profile: &entities.FinancialProfile{
				UserID:               userID,
				PrimaryCurrency:      "USD",
				IncomeFrequency:      "unknown",
				MonthlyIncome:        decimal.Zero,
				MonthlyFixedCosts:    decimal.NewFromInt(80),
				MonthlySavingsTarget: decimal.NewFromInt(40),
				UpdatedAt:            time.Now().AddDate(0, 0, -120),
			},
		},
		savingsGoalStore: govSavingsGoalStoreFake{
			goal: &SavingsGoal{Name: "Emergency Fund", Target: "150.00"},
		},
		logger: zap.NewNop(),
	}

	result, err := o.executeFinancialAdvice(context.Background(), userID, map[string]interface{}{
		"intent":          "transfer",
		"proposed_amount": 50.0,
	})
	require.NoError(t, err)
	require.Equal(t, "critical", result["overall_status"])

	checks, ok := result["checks"].([]FinancialRuleCheck)
	require.True(t, ok)
	require.NotEmpty(t, checks)

	codes := make(map[string]bool, len(checks))
	for _, check := range checks {
		codes[check.Code] = true
	}

	require.True(t, codes["low_balance"])
	require.True(t, codes["missing_income"])
	require.True(t, codes["stale_profile"])
	require.True(t, codes["savings_drift"])
	require.True(t, codes["goal_drift"])
	require.True(t, codes["risky_transfer"])
}

func TestExecuteFinancialTimelineMergesEvents(t *testing.T) {
	userID := uuid.New()
	now := time.Now().UTC()
	o := &Orchestrator{
		depositHistory: govDepositHistoryFake{
			deposits: []*entities.Deposit{
				{UserID: userID, Amount: decimal.NewFromInt(100), Status: "confirmed", Chain: entities.ChainETH, TxHash: "0xabc", CreatedAt: now.Add(-2 * time.Hour)},
			},
		},
		withdrawalHistory: govWithdrawalHistoryFake{
			withdrawals: []*entities.Withdrawal{
				{UserID: userID, Amount: decimal.NewFromInt(25), Currency: "USD", WithdrawalType: entities.WithdrawalTypeFiat, DestinationType: entities.DestinationTypeBankAccount, Status: entities.WithdrawalStatusCompleted, CreatedAt: now.Add(-3 * time.Hour)},
			},
		},
		actionHistory: govActionHistoryFake{
			actions: []*entities.ActionAuditEntry{
				{UserID: userID, Action: ToolSetBudget, Status: "executed", CreatedAt: now.Add(-1 * time.Hour)},
			},
		},
		logger: zap.NewNop(),
	}

	result, err := o.executeFinancialTimeline(context.Background(), userID, map[string]interface{}{
		"days":  7,
		"limit": 10,
	})
	require.NoError(t, err)

	events, ok := result["events"].([]FinancialTimelineEvent)
	require.True(t, ok)
	require.Len(t, events, 3)
	types := make(map[string]bool, len(events))
	for _, event := range events {
		types[event.Type] = true
	}
	require.True(t, types["agent_action"])
	require.True(t, types["income_received"])
	require.True(t, types["withdrawal"])
}

func TestExecuteFinancialAdviceInvestmentIntentForcesCaution(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{
			spend: decimal.NewFromInt(200),
			stash: decimal.NewFromInt(300),
		},
		logger: zap.NewNop(),
	}

	result, err := o.executeFinancialAdvice(context.Background(), userID, map[string]interface{}{
		"intent": "investment",
	})
	require.NoError(t, err)
	require.Equal(t, "caution", result["overall_status"])
	require.Contains(t, result["summary"], "safety")
}

func TestExecuteFinancialAdviceChecksContainExplainabilityFields(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{
			spend: decimal.NewFromInt(10),
			stash: decimal.NewFromInt(5),
		},
		budgetProvider: govBudgetProviderFake{
			budget: &entities.SpendingBudget{
				UserID:       userID,
				MonthlyLimit: decimal.NewFromInt(50),
				Currency:     "USD",
			},
		},
		logger: zap.NewNop(),
	}

	result, err := o.executeFinancialAdvice(context.Background(), userID, nil)
	require.NoError(t, err)
	checks, ok := result["checks"].([]FinancialRuleCheck)
	require.True(t, ok)
	require.NotEmpty(t, checks)

	for _, check := range checks {
		require.NotEmpty(t, check.DataUsed)
		require.NotNil(t, check.Evidence)
	}
}

package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type auditSpendingFake struct {
	flow    *entities.MoneyFlowSummary
	summary *spendingsvc.Summary
}

func (f auditSpendingFake) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spendingsvc.Summary, error) {
	return f.summary, nil
}

func (f auditSpendingFake) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return nil, nil
}

func (f auditSpendingFake) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return f.flow, nil
}

func (f auditSpendingFake) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

type auditRecurringFake struct {
	expenses []RecurringExpense
}

func (f auditRecurringFake) DetectRecurring(_ context.Context, _ uuid.UUID) ([]RecurringExpense, error) {
	return f.expenses, nil
}

func TestExecuteFinancialAuditFindsContradictionsAndActions(t *testing.T) {
	userID := uuid.New()
	dueDay := 15
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{spend: decimalRequire("200"), stash: decimalRequire("10")},
		spending: auditSpendingFake{
			flow: &entities.MoneyFlowSummary{
				TotalDeposits:    decimalRequire("1000"),
				TotalWithdrawals: decimalRequire("300"),
				TotalCardSpend:   decimalRequire("500"),
				TotalP2P:         decimalRequire("100"),
				TotalReceipts:    decimalRequire("50"),
				DepositCount:     2,
				WithdrawalCount:  3,
				CardSpendCount:   18,
				P2PCount:         4,
				ReceiptCount:     1,
			},
			summary: &spendingsvc.Summary{
				Total:    decimalRequire("950"),
				TxCount:  26,
				DailyAvg: decimalRequire("31.67"),
				Categories: []entities.SpendingByCategory{
					{Category: "Food & Dining", Total: decimalRequire("350"), Count: 12},
					{Category: "Transportation", Total: decimalRequire("120"), Count: 8},
				},
				Merchants: []entities.SpendingByMerchant{
					{Merchant: "Very Long Restaurant Merchant Name", Total: decimalRequire("180"), Count: 5},
				},
			},
		},
		financialProfile: govFinancialProfileFake{profile: &entities.FinancialProfile{
			UserID:               userID,
			PrimaryCurrency:      "USD",
			MonthlySavingsTarget: decimalRequire("250"),
			FinancialGoal:        "Build emergency fund",
		}},
		budgetProvider: govBudgetProviderFake{budget: &entities.SpendingBudget{
			UserID:       userID,
			MonthlyLimit: decimalRequire("800"),
			Currency:     "USD",
		}},
		obligations: operatingPlanObligationsFake{obligations: []entities.FinancialObligation{
			{
				ID:       uuid.New(),
				UserID:   userID,
				Type:     entities.ObligationTypeRent,
				Name:     "Rent",
				Amount:   decimalRequire("300"),
				Currency: "USD",
				Cadence:  entities.ObligationCadenceMonthly,
				DueDay:   &dueDay,
				Priority: entities.ObligationPriorityCritical,
				Status:   entities.ObligationStatusActive,
			},
		}},
		recurringDetector: auditRecurringFake{expenses: []RecurringExpense{
			{Merchant: "Netflix", Frequency: "monthly", AvgAmount: decimalRequire("250"), Total: decimalRequire("250"), Count: 1},
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeFinancialAudit(context.Background(), userID, map[string]interface{}{"intensity": "hard"})
	require.NoError(t, err)
	require.Equal(t, true, result["audit_mode"])
	require.Equal(t, "hard", result["intensity"])

	snapshot := result["snapshot"].(map[string]interface{})
	require.Equal(t, "950.00", snapshot["total_money_out"])
	require.Equal(t, "50.00", snapshot["net_flow"])

	score := result["score"].(map[string]interface{})
	require.Equal(t, "needs_intervention", score["status"])

	contradictions := result["contradictions"].([]map[string]interface{})
	require.Contains(t, auditCodes(contradictions), "goal_gap")
	require.Contains(t, auditCodes(contradictions), "category_dominates")
	require.Contains(t, auditCodes(contradictions), "budget_mismatch")
	require.Contains(t, auditCodes(contradictions), "obligation_shortfall")
	require.Contains(t, auditCodes(contradictions), "recurring_drag")

	actions := result["do_this_today"].([]map[string]interface{})
	require.NotEmpty(t, actions)
	require.Equal(t, true, actions[0]["requires_confirmation"])
}

func TestFinancialAuditToolDispatchesPublicly(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{spend: decimalRequire("100"), stash: decimalRequire("100")},
		spending: auditSpendingFake{
			flow: &entities.MoneyFlowSummary{TotalDeposits: decimalRequire("500")},
			summary: &spendingsvc.Summary{
				Total:    decimal.Zero,
				TxCount:  0,
				DailyAvg: decimal.Zero,
			},
		},
		logger: zap.NewNop(),
	}

	result, err := o.ExecuteToolPublic(context.Background(), userID, infraToolCall(ToolGetFinancialAudit, map[string]interface{}{"period": "this_month"}))
	require.NoError(t, err)
	require.Equal(t, true, result["audit_mode"])
}

func TestFinancialAuditBuildsInlineCard(t *testing.T) {
	result := map[string]interface{}{
		"score": map[string]interface{}{
			"total":  42,
			"status": "needs_intervention",
		},
		"snapshot": map[string]interface{}{
			"money_in":          "1000.00",
			"digital_money_out": "900.00",
			"receipt_cash_out":  "50.00",
			"total_money_out":   "950.00",
			"net_flow":          "50.00",
		},
		"the_damage": map[string]interface{}{
			"primary_issue": "Budget pace is ahead of where it should be.",
		},
		"the_pattern": []string{"For every $100 in, $95.00 left."},
		"contradictions": []map[string]interface{}{
			{"code": "goal_gap", "take": "The current pace is not funding the goal."},
		},
		"top_spending_categories": []map[string]interface{}{
			{"category": "Jollof Fund", "total": "350.00", "count": 12},
		},
		"risk_flags": []map[string]interface{}{
			{"code": "thin_spend_balance", "severity": "high", "title": "Spend balance is thin"},
		},
		"do_this_today": []map[string]interface{}{
			{"title": "Set a monthly spending ceiling", "requires_confirmation": true},
		},
	}

	cards := buildCardsFromToolResults([]ToolResult{{Name: ToolGetFinancialAudit, Result: result}})
	require.Len(t, cards, 1)
	require.Equal(t, "financial_audit", cards[0].Type)
	require.Equal(t, "Miriam Audit", cards[0].Title)

	data, ok := cards[0].Data.(map[string]interface{})
	require.True(t, ok)
	require.NotEmpty(t, data["metrics"])
	require.NotEmpty(t, data["breakdown"])
	require.NotEmpty(t, data["contradictions"])
}

func auditCodes(items []map[string]interface{}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		if code, ok := item["code"].(string); ok {
			codes = append(codes, code)
		}
	}
	return codes
}

func infraToolCall(name string, args map[string]interface{}) infraai.ToolCall {
	return infraai.ToolCall{ID: name + "-test", Name: name, Arguments: args}
}

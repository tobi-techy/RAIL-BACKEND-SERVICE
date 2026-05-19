package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type obligationManagerFake struct {
	obligations []entities.FinancialObligation
	markedID    uuid.UUID
}

func (f *obligationManagerFake) List(_ context.Context, _ uuid.UUID, _, _ string) ([]entities.FinancialObligation, error) {
	return f.obligations, nil
}

func (f *obligationManagerFake) MarkPaid(_ context.Context, _ uuid.UUID, id uuid.UUID) (*entities.FinancialObligation, error) {
	f.markedID = id
	return &entities.FinancialObligation{ID: id, Status: entities.ObligationStatusPaid}, nil
}

func (f *obligationManagerFake) MarkCancelled(_ context.Context, _ uuid.UUID, id uuid.UUID) (*entities.FinancialObligation, error) {
	f.markedID = id
	return &entities.FinancialObligation{ID: id, Status: entities.ObligationStatusCancelled}, nil
}

type obligationMatchSpendingFake struct {
	transactions []entities.SpendingTransaction
}

func (f obligationMatchSpendingFake) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spendingsvc.Summary, error) {
	return &spendingsvc.Summary{}, nil
}

func (f obligationMatchSpendingFake) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return f.transactions, nil
}

func (f obligationMatchSpendingFake) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return &entities.MoneyFlowSummary{}, nil
}

func (f obligationMatchSpendingFake) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

func TestListFinancialObligationsReturnsMonthlyContext(t *testing.T) {
	userID := uuid.New()
	manager := &obligationManagerFake{obligations: []entities.FinancialObligation{
		{
			ID:       uuid.New(),
			UserID:   userID,
			Type:     entities.ObligationTypeRent,
			Name:     "Rent",
			Amount:   decimalRequire("800"),
			Currency: "USD",
			Cadence:  entities.ObligationCadenceMonthly,
			Priority: entities.ObligationPriorityCritical,
			Status:   entities.ObligationStatusActive,
		},
	}}
	o := &Orchestrator{obligationManager: manager, logger: zap.NewNop()}

	result, err := o.executeListFinancialObligations(context.Background(), userID, map[string]interface{}{})
	require.NoError(t, err)
	require.Equal(t, 1, result["count"])
	require.Equal(t, "800.00", result["monthly_total"])
	require.Equal(t, "800.00", result["critical_monthly_total"])
}

func TestMarkObligationPaidUsesPendingConfirmation(t *testing.T) {
	userID := uuid.New()
	convID := uuid.New()
	obligationID := uuid.New()
	manager := &obligationManagerFake{}
	o := &Orchestrator{
		obligationManager: manager,
		pending:           newInMemoryPendingActions(),
		logger:            zap.NewNop(),
	}

	result, err := o.createMarkObligationPaidAction(context.Background(), userID, convID, map[string]interface{}{
		"obligation_id": obligationID.String(),
		"name":          "Rent",
	})
	require.NoError(t, err)
	require.Equal(t, true, result["action_required"])

	confirmed, err := o.ConfirmAction(context.Background(), userID, convID)
	require.NoError(t, err)
	require.Equal(t, ToolMarkObligationPaid, confirmed.Action)
	require.Equal(t, obligationID, manager.markedID)
}

func TestFindObligationPaymentMatchesSuggestsMarkPaid(t *testing.T) {
	userID := uuid.New()
	obligationID := uuid.New()
	manager := &obligationManagerFake{obligations: []entities.FinancialObligation{
		{
			ID:       obligationID,
			UserID:   userID,
			Type:     entities.ObligationTypeRent,
			Name:     "Rent",
			Amount:   decimalRequire("800"),
			Currency: "USD",
			Cadence:  entities.ObligationCadenceMonthly,
			Priority: entities.ObligationPriorityCritical,
			Status:   entities.ObligationStatusActive,
		},
	}}
	o := &Orchestrator{
		obligationManager: manager,
		spending: obligationMatchSpendingFake{transactions: []entities.SpendingTransaction{
			{
				Date:     "2026-05-01",
				Amount:   decimalRequire("800"),
				Category: "housing",
				Source:   "May Rent",
			},
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeFindObligationPaymentMatches(context.Background(), userID, map[string]interface{}{})
	require.NoError(t, err)
	require.Equal(t, 1, result["count"])

	matches := result["matches"].([]map[string]interface{})
	require.Equal(t, obligationID.String(), matches[0]["obligation_id"])
	require.Equal(t, "high", matches[0]["confidence"])
	action := matches[0]["suggested_action"].(map[string]interface{})
	require.Equal(t, ToolMarkObligationPaid, action["type"])
}

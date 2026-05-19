package ai

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type subscriptionRecurringFake struct {
	expenses []RecurringExpense
}

func (f subscriptionRecurringFake) DetectRecurring(_ context.Context, _ uuid.UUID) ([]RecurringExpense, error) {
	return f.expenses, nil
}

type subscriptionObligationCreatorFake struct {
	req AIServiceObligationRequest
}

func (f *subscriptionObligationCreatorFake) CreateObligationFromAI(_ context.Context, userID uuid.UUID, req AIServiceObligationRequest) (*entities.FinancialObligation, error) {
	f.req = req
	return &entities.FinancialObligation{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     req.Type,
		Name:     req.Name,
		Amount:   req.Amount,
		Currency: req.Currency,
		Cadence:  req.Cadence,
		Priority: req.Priority,
		Status:   req.Status,
	}, nil
}

func TestGetSubscriptionsUsesRecurringDetectorCandidates(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		recurringDetector: subscriptionRecurringFake{expenses: []RecurringExpense{
			{
				Merchant:  "Netflix",
				Frequency: "monthly",
				AvgAmount: decimalRequire("15.99"),
				Total:     decimalRequire("47.97"),
				FirstSeen: "2026-01-01",
				LastSeen:  "2026-03-01",
				Count:     3,
			},
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeGetSubscriptions(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, "15.99", result["total_monthly"])
	require.Equal(t, "recurring_detector", result["source"])

	candidates := result["candidates"].([]map[string]interface{})
	require.Len(t, candidates, 1)
	require.Equal(t, "Netflix", candidates[0]["name"])
	require.NotEmpty(t, candidates[0]["candidate_id"])
	require.NotEmpty(t, candidates[0]["recommended_actions"])
}

func TestProtectSubscriptionCreatesObligationAfterConfirmation(t *testing.T) {
	userID := uuid.New()
	convID := uuid.New()
	creator := &subscriptionObligationCreatorFake{}
	o := &Orchestrator{
		obligationCreator: creator,
		pending:           newInMemoryPendingActions(),
		logger:            zap.NewNop(),
	}

	result, err := o.createProtectSubscriptionAction(context.Background(), userID, convID, map[string]interface{}{
		"name":      "Netflix",
		"amount":    float64(15.99),
		"currency":  "USD",
		"frequency": "monthly",
	})
	require.NoError(t, err)
	require.Equal(t, true, result["action_required"])

	confirmed, err := o.ConfirmAction(context.Background(), userID, convID)
	require.NoError(t, err)
	require.Equal(t, ToolProtectSubscription, confirmed.Action)
	require.Equal(t, entities.ObligationTypeSubscription, creator.req.Type)
	require.Equal(t, entities.ObligationStatusActive, creator.req.Status)
	require.Equal(t, entities.ObligationCadenceMonthly, creator.req.Cadence)
}

func TestMarkSubscriptionCancelledCreatesCancelledRecord(t *testing.T) {
	userID := uuid.New()
	convID := uuid.New()
	creator := &subscriptionObligationCreatorFake{}
	o := &Orchestrator{
		obligationCreator: creator,
		pending:           newInMemoryPendingActions(),
		logger:            zap.NewNop(),
	}

	_, err := o.createMarkSubscriptionCancelledAction(context.Background(), userID, convID, map[string]interface{}{
		"name":      "Canva",
		"amount":    float64(12),
		"currency":  "USD",
		"frequency": "monthly",
	})
	require.NoError(t, err)

	confirmed, err := o.ConfirmAction(context.Background(), userID, convID)
	require.NoError(t, err)
	require.Equal(t, ToolMarkSubscriptionCancelled, confirmed.Action)
	require.Equal(t, entities.ObligationStatusCancelled, creator.req.Status)
}

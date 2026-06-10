package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Mocks ---

type mockBalanceProvider struct {
	spend decimal.Decimal
	stash decimal.Decimal
}

func (m *mockBalanceProvider) GetSpendBalance(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.spend, nil
}
func (m *mockBalanceProvider) GetStashBalance(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.stash, nil
}

type mockTransferExecutor struct {
	calls []transferCall
}

type transferCall struct {
	userID uuid.UUID
	from   string
	to     string
	amount decimal.Decimal
}

func (m *mockTransferExecutor) TransferBetweenStashes(_ context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	m.calls = append(m.calls, transferCall{userID: userID, from: from, to: to, amount: amount})
	return nil
}

type mockGoalTransferExecutor struct {
	transfers      []goalTransferCall
	balances       map[uuid.UUID]decimal.Decimal
	totalAllocated decimal.Decimal
}

type goalTransferCall struct {
	userID uuid.UUID
	goalID uuid.UUID
	amount decimal.Decimal
}

func (m *mockGoalTransferExecutor) TransferSpendToGoal(_ context.Context, userID, goalID uuid.UUID, amount decimal.Decimal, _ string) error {
	m.transfers = append(m.transfers, goalTransferCall{userID: userID, goalID: goalID, amount: amount})
	return nil
}

func (m *mockGoalTransferExecutor) GetGoalBalance(_ context.Context, _ uuid.UUID, goalID uuid.UUID) (decimal.Decimal, error) {
	if bal, ok := m.balances[goalID]; ok {
		return bal, nil
	}
	return decimal.Zero, nil
}

func (m *mockGoalTransferExecutor) GetTotalGoalAllocated(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.totalAllocated, nil
}

type mockGoalContributor struct {
	contributions []goalContribCall
}

type goalContribCall struct {
	userID uuid.UUID
	goalID uuid.UUID
	amount decimal.Decimal
}

func (m *mockGoalContributor) RecordAutomationContribution(_ context.Context, userID, goalID uuid.UUID, amount decimal.Decimal) error {
	m.contributions = append(m.contributions, goalContribCall{userID: userID, goalID: goalID, amount: amount})
	return nil
}

type mockGoalChecker struct {
	reached map[uuid.UUID]bool
}

func (m *mockGoalChecker) IsGoalReached(_ context.Context, _ uuid.UUID, goalID uuid.UUID) (bool, error) {
	return m.reached[goalID], nil
}

func (m *mockGoalChecker) GetGoalTarget(_ context.Context, goalID uuid.UUID) (decimal.Decimal, error) {
	return decimal.NewFromInt(1000), nil // default target for tests
}

// --- Tests ---

func TestExecuteAction_GoalLinkedTransferRoutesToGoalAccount(t *testing.T) {
	goalID := uuid.New()
	userID := uuid.New()

	transfer := &mockTransferExecutor{}
	goalTransfer := &mockGoalTransferExecutor{}
	goalContrib := &mockGoalContributor{}

	svc := &Service{
		transfer:        transfer,
		goalTransfer:    goalTransfer,
		goalContributor: goalContrib,
		logger:          zap.NewNop(),
	}

	now := time.Now().UTC()
	actionConfig := StampTransferConsent(map[string]interface{}{"amount": float64(50)}, now)
	actionCfgBytes, _ := json.Marshal(actionConfig)

	automation := &entities.MiriamAutomation{
		ID:            uuid.New(),
		UserID:        userID,
		ActionType:    entities.ActionTransferToStash,
		ActionConfig:  actionCfgBytes,
		SavingsGoalID: &goalID,
	}

	err := svc.executeAction(context.Background(), automation)
	require.NoError(t, err)

	// Should have routed to goal, not generic stash
	assert.Empty(t, transfer.calls, "should NOT transfer to generic stash")
	require.Len(t, goalTransfer.transfers, 1)
	assert.Equal(t, goalID, goalTransfer.transfers[0].goalID)
	assert.Equal(t, userID, goalTransfer.transfers[0].userID)
	assert.True(t, goalTransfer.transfers[0].amount.Equal(decimal.NewFromInt(50)))

	// Should record contribution
	require.Len(t, goalContrib.contributions, 1)
	assert.Equal(t, goalID, goalContrib.contributions[0].goalID)
	assert.True(t, goalContrib.contributions[0].amount.Equal(decimal.NewFromInt(50)))
}

func TestExecuteAction_NoGoalTransferFallsBackToGenericStash(t *testing.T) {
	userID := uuid.New()

	transfer := &mockTransferExecutor{}

	svc := &Service{
		transfer: transfer,
		logger:   zap.NewNop(),
	}

	now := time.Now().UTC()
	actionConfig := StampTransferConsent(map[string]interface{}{"amount": float64(30)}, now)
	actionCfgBytes, _ := json.Marshal(actionConfig)

	automation := &entities.MiriamAutomation{
		ID:         uuid.New(),
		UserID:     userID,
		ActionType: entities.ActionTransferToStash,
		ActionConfig: actionCfgBytes,
	}

	err := svc.executeAction(context.Background(), automation)
	require.NoError(t, err)

	require.Len(t, transfer.calls, 1)
	assert.Equal(t, "spend", transfer.calls[0].from)
	assert.Equal(t, "stash", transfer.calls[0].to)
	assert.True(t, transfer.calls[0].amount.Equal(decimal.NewFromInt(30)))
}

func TestExecuteAction_GoalLinkedWithoutGoalTransferWiredFallsBack(t *testing.T) {
	goalID := uuid.New()
	userID := uuid.New()

	transfer := &mockTransferExecutor{}

	// goalTransfer is nil — falls back to generic stash
	svc := &Service{
		transfer: transfer,
		logger:   zap.NewNop(),
	}

	now := time.Now().UTC()
	actionConfig := StampTransferConsent(map[string]interface{}{"amount": float64(25)}, now)
	actionCfgBytes, _ := json.Marshal(actionConfig)

	automation := &entities.MiriamAutomation{
		ID:            uuid.New(),
		UserID:        userID,
		ActionType:    entities.ActionTransferToStash,
		ActionConfig:  actionCfgBytes,
		SavingsGoalID: &goalID,
	}

	err := svc.executeAction(context.Background(), automation)
	require.NoError(t, err)

	// Should fall back to generic stash
	require.Len(t, transfer.calls, 1)
	assert.Equal(t, "spend", transfer.calls[0].from)
	assert.Equal(t, "stash", transfer.calls[0].to)
}

func TestBillShield_RespectsGoalAllocatedFunds(t *testing.T) {
	userID := uuid.New()
	dueDayInFuture := time.Now().AddDate(0, 0, 3).Day()

	mockBalance := &mockBalanceProvider{
		spend: decimal.NewFromInt(20),  // $20 in spend
		stash: decimal.NewFromInt(200), // $200 in stash
	}
	mockTransfer := &mockTransferExecutor{}
	goalTransfer := &mockGoalTransferExecutor{
		totalAllocated: decimal.NewFromInt(180), // $180 allocated to goals
	}

	mockObligation := &mockObligationProvider{
		obligations: []entities.FinancialObligation{
			{ID: uuid.New(), Amount: decimal.NewFromInt(100), DueDay: &dueDayInFuture},
		},
	}

	svc := &Service{
		balance:      mockBalance,
		transfer:     mockTransfer,
		goalTransfer: goalTransfer,
		obligations:  mockObligation,
		logger:       zap.NewNop(),
	}

	err := svc.EvaluateBillShield(context.Background(), userID)
	require.NoError(t, err)

	// Shortfall is $100 - $20 = $80
	// Available stash = $200 - $180 (goals) = $20
	// Should only transfer $20 (the unallocated portion)
	require.Len(t, mockTransfer.calls, 1)
	assert.True(t, mockTransfer.calls[0].amount.Equal(decimal.NewFromInt(20)))
}

func TestBillShield_FullStashWithoutGoalsTransfersNormally(t *testing.T) {
	userID := uuid.New()
	dueDayInFuture := time.Now().AddDate(0, 0, 3).Day()

	mockBalance := &mockBalanceProvider{
		spend: decimal.NewFromInt(20),
		stash: decimal.NewFromInt(200),
	}
	mockTransfer := &mockTransferExecutor{}

	mockObligation := &mockObligationProvider{
		obligations: []entities.FinancialObligation{
			{ID: uuid.New(), Amount: decimal.NewFromInt(100), DueDay: &dueDayInFuture},
		},
	}

	svc := &Service{
		balance:     mockBalance,
		transfer:    mockTransfer,
		obligations: mockObligation,
		logger:      zap.NewNop(),
	}

	err := svc.EvaluateBillShield(context.Background(), userID)
	require.NoError(t, err)

	// No goals — full stash available: shortfall = $80, stash = $200 → transfer $80
	require.Len(t, mockTransfer.calls, 1)
	assert.True(t, mockTransfer.calls[0].amount.Equal(decimal.NewFromInt(80)))
}

// mockObligationProvider implements ObligationProvider for testing.
type mockObligationProvider struct {
	obligations []entities.FinancialObligation
}

func (m *mockObligationProvider) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	return m.obligations, nil
}

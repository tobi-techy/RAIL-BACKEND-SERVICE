package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/execution"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- mocks ---

type mockFundsTransferer struct {
	calls              int
	transferErr        error
	spendBalance       decimal.Decimal
	stashBalance       decimal.Decimal
	lastFrom           string
	lastTo             string
	lastAmount         decimal.Decimal
	lastIdempotencyKey string
}

func (m *mockFundsTransferer) TransferSpendToStash(_ context.Context, _ uuid.UUID, amount decimal.Decimal, key string) error {
	m.calls++
	m.lastFrom = "spend"
	m.lastTo = "stash"
	m.lastAmount = amount
	m.lastIdempotencyKey = key
	return m.transferErr
}

func (m *mockFundsTransferer) TransferStashToSpend(_ context.Context, _ uuid.UUID, amount decimal.Decimal, key string) error {
	m.calls++
	m.lastFrom = "stash"
	m.lastTo = "spend"
	m.lastAmount = amount
	m.lastIdempotencyKey = key
	return m.transferErr
}

func (m *mockFundsTransferer) GetSpendBalance(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.spendBalance, nil
}

func (m *mockFundsTransferer) GetStashBalance(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.stashBalance, nil
}

type mockStepUpVerifier struct {
	validTokens map[string]bool
	err         error
	calls       int
}

func (m *mockStepUpVerifier) VerifyStepUp(_ context.Context, _ uuid.UUID, token string) (bool, error) {
	m.calls++
	if m.err != nil {
		return false, m.err
	}
	if m.validTokens != nil {
		return m.validTokens[token], nil
	}
	return token == "valid-token", nil
}

func newTestAdapter(t *testing.T) *AgentAdapter {
	t.Helper()
	return &AgentAdapter{
		pending: newInMemoryPendingActions(),
		logger:  zap.NewNop(),
	}
}

func stageTransferAction(t *testing.T, o *AgentAdapter, userID, convID uuid.UUID, amount float64) {
	t.Helper()
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolTransferFunds,
		Description:    "Move $50.00 from spend to stash",
		Params: map[string]interface{}{
			"from":   "spend",
			"to":     "stash",
			"amount": decimal.NewFromFloat(amount).StringFixed(2),
		},
		ExpiresAt: time.Now().Add(5 * time.Minute),
		CreatedAt: time.Now(),
	}
	require.NoError(t, o.pending.Set(context.Background(), convID, action))
}

func stageNonFundAction(t *testing.T, o *AgentAdapter, userID, convID uuid.UUID) {
	t.Helper()
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolSetSavingsGoal,
		Description:    "Set savings goal 'Emergency Fund' for $1000.00",
		Params: map[string]interface{}{
			"name":   "Emergency Fund",
			"target": "1000.00",
		},
		ExpiresAt: time.Now().Add(5 * time.Minute),
		CreatedAt: time.Now(),
	}
	require.NoError(t, o.pending.Set(context.Background(), convID, action))
}

// --- IsFundMovingAction tests ---

func TestIsFundMovingAction_CoversAllRegisteredTools(t *testing.T) {
	fundMoving := []string{
		ToolTransferFunds, ToolInitiateWithdrawal,
		ToolExecuteInvestment, ToolOptimizeYield, ToolCopyTrader,
		ToolSetupBillAutopay, ToolPayBill, ToolAutomateBill,
		"send_money", ToolSplitReceipt, ToolCreateAutomation,
	}
	for _, name := range fundMoving {
		assert.True(t, IsFundMovingAction(name), "%s should be fund-moving", name)
	}
}

func TestIsFundMovingAction_RejectsNonFundActions(t *testing.T) {
	nonFund := []string{
		"get_account_summary", "list_automations", "set_savings_goal",
		"get_spending_summary", "create_obligation_reminder",
		"set_budget", "send_report", "get_balance",
	}
	for _, name := range nonFund {
		assert.False(t, IsFundMovingAction(name), "%s should NOT be fund-moving", name)
	}
}

func TestRegisterFundMovingAction(t *testing.T) {
	original := IsFundMovingAction("custom_new_fund_tool")
	assert.False(t, original)

	execution.RegisterFundMovingAction("custom_new_fund_tool")
	assert.True(t, IsFundMovingAction("custom_new_fund_tool"))

	t.Cleanup(func() {
		execution.FundMovingActionsMu.Lock()
		defer execution.FundMovingActionsMu.Unlock()
		delete(execution.FundMovingActions, "custom_new_fund_tool")
	})
}

// --- Step-up context helper tests ---

func TestWithStepUpToken_RoundTrip(t *testing.T) {
	ctx := execution.WithStepUpToken(context.Background(), "abc-123")
	assert.Equal(t, "abc-123", execution.StepUpTokenFromContext(ctx))
}

func TestStepUpTokenFromContext_Empty(t *testing.T) {
	assert.Equal(t, "", execution.StepUpTokenFromContext(context.Background()))
}

func TestErrStepUpRequired_IsSentinel(t *testing.T) {
	assert.True(t, errors.Is(execution.ErrStepUpRequired, execution.ErrStepUpRequired))
	wrapped := fmt.Errorf("wrapper: %w", execution.ErrStepUpRequired)
	assert.True(t, errors.Is(wrapped, execution.ErrStepUpRequired))
}

// --- ConfirmAction step-up tests ---

func TestConfirmAction_RefusesFundMoveWithoutStepUpToken(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	o.stepUpVerifier = &mockStepUpVerifier{} // wired but no token in context

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	_, err := o.ConfirmAction(context.Background(), userID, convID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, execution.ErrStepUpRequired))

	// Money should NOT have moved
	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 0, ft.calls, "transfer should not have been called")
}

func TestConfirmAction_RefusesFundMoveWithInvalidToken(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	o.stepUpVerifier = &mockStepUpVerifier{
		validTokens: map[string]bool{"valid-token": true},
	}

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	ctx := execution.WithStepUpToken(context.Background(), "invalid-token")
	_, err := o.ConfirmAction(ctx, userID, convID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, execution.ErrStepUpRequired))

	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 0, ft.calls, "transfer should not have been called on invalid token")
}

func TestConfirmAction_RefusesFundMoveWhenVerifierNil(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	// stepUpVerifier is nil — fail-closed

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	_, err := o.ConfirmAction(context.Background(), userID, convID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, execution.ErrStepUpRequired))

	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 0, ft.calls, "transfer should not have been called with nil verifier")
}

func TestConfirmAction_RefusesFundMoveWhenVerifierErrors(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	o.stepUpVerifier = &mockStepUpVerifier{
		err: errors.New("redis down"),
	}

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	ctx := execution.WithStepUpToken(context.Background(), "any-token")
	_, err := o.ConfirmAction(ctx, userID, convID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, execution.ErrStepUpRequired))

	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 0, ft.calls, "transfer should not have been called when verifier errors")
}

func TestConfirmAction_ExecutesFundMoveWithValidStepUp(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	o.stepUpVerifier = &mockStepUpVerifier{
		validTokens: map[string]bool{"valid-token": true},
	}

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	ctx := execution.WithStepUpToken(context.Background(), "valid-token")
	action, err := o.ConfirmAction(ctx, userID, convID)
	require.NoError(t, err)
	assert.NotNil(t, action)
	assert.Equal(t, ToolTransferFunds, action.Action)

	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 1, ft.calls, "transfer should have been called once")
	assert.Equal(t, "spend", ft.lastFrom)
	assert.Equal(t, "stash", ft.lastTo)
	assert.True(t, ft.lastAmount.Equal(decimal.NewFromFloat(50)))
}

func TestConfirmAction_NonFundMoveExecutesWithoutStepUp(t *testing.T) {
	o := newTestAdapter(t)
	o.savingsGoalStore = &mockSavingsGoalStore{}
	// No stepUpVerifier wired — but non-fund action shouldn't need it

	userID := uuid.New()
	convID := uuid.New()
	stageNonFundAction(t, o, userID, convID)

	action, err := o.ConfirmAction(context.Background(), userID, convID)
	require.NoError(t, err)
	assert.NotNil(t, action)
	assert.Equal(t, ToolSetSavingsGoal, action.Action)
}

func TestConfirmAction_RefusesFundMoveForWrongUser(t *testing.T) {
	o := newTestAdapter(t)
	o.stepUpVerifier = &mockStepUpVerifier{}

	userID := uuid.New()
	otherUser := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	_, err := o.ConfirmAction(context.Background(), otherUser, convID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong")
}

// --- Voice direct path tests ---

func TestExecuteActionToolDirect_BlocksFundMove(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}

	userID := uuid.New()
	result, err := o.executeActionToolDirect(context.Background(), userID, infraai.ToolCall{
		Name: ToolTransferFunds,
		Arguments: map[string]interface{}{
			"from":   "spend",
			"to":     "stash",
			"amount": float64(50),
		},
	})
	require.NoError(t, err)
	assert.Contains(t, result["error"], "Face ID")

	ft := o.fundsTransferer.(*mockFundsTransferer)
	assert.Equal(t, 0, ft.calls, "voice direct should not execute fund moves")
}

func TestExecuteActionToolDirect_BlocksAllFundMovingActions(t *testing.T) {
	o := newTestAdapter(t)

	fundMovingTools := []string{
		ToolTransferFunds, ToolInitiateWithdrawal, "send_money",
		ToolSplitReceipt, ToolPayBill, ToolAutomateBill,
	}
	for _, name := range fundMovingTools {
		t.Run(name, func(t *testing.T) {
			result, err := o.executeActionToolDirect(context.Background(), uuid.New(), infraai.ToolCall{
				Name:      name,
				Arguments: map[string]interface{}{},
			})
			require.NoError(t, err)
			assert.Contains(t, result["error"], "Face ID",
				"%s should be blocked in voice direct path", name)
		})
	}
}

// --- Platform adapter defense-in-depth test ---
// (The full ConfirmPlatformAction test lives in misc_adapters, but we verify
// here that the core refuses fund moves even without a step-up token — which
// is the second layer of defense the platform adapter relies on.)

func TestConfirmAction_CoreRefusesFundMoveEvenWithValidVerifierButNoToken(t *testing.T) {
	o := newTestAdapter(t)
	o.fundsTransferer = &mockFundsTransferer{
		spendBalance: decimal.NewFromFloat(1000),
		stashBalance: decimal.Zero,
	}
	// Verifier is wired but we don't inject a token (simulating a platform
	// adapter that bypassed the handler's token injection).
	o.stepUpVerifier = &mockStepUpVerifier{
		validTokens: map[string]bool{"valid-token": true},
	}

	userID := uuid.New()
	convID := uuid.New()
	stageTransferAction(t, o, userID, convID, 50)

	// No WithStepUpToken — this is what the platform adapter would look like
	_, err := o.ConfirmAction(context.Background(), userID, convID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, execution.ErrStepUpRequired))

	// Verifier should not even have been called (no token to verify)
	sv := o.stepUpVerifier.(*mockStepUpVerifier)
	assert.Equal(t, 0, sv.calls, "verifier should not be called when token is empty")
}

// --- mock savings goal store ---

type mockSavingsGoalStore struct {
	goal *SavingsGoal
}

func (m *mockSavingsGoalStore) Set(_ context.Context, _ uuid.UUID, goal *SavingsGoal) error {
	m.goal = goal
	return nil
}

func (m *mockSavingsGoalStore) Get(_ context.Context, _ uuid.UUID) (*SavingsGoal, error) {
	return m.goal, nil
}

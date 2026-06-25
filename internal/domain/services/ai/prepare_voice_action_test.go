package ai

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// prepareVoiceFundsTransfererFake is a minimal FundsTransferer for building a
// transfer pending action without hitting the ledger.
type prepareVoiceFundsTransfererFake struct {
	spend decimal.Decimal
	stash decimal.Decimal
}

func (f *prepareVoiceFundsTransfererFake) TransferSpendToStash(context.Context, uuid.UUID, decimal.Decimal, string) error {
	return nil
}

func (f *prepareVoiceFundsTransfererFake) TransferStashToSpend(context.Context, uuid.UUID, decimal.Decimal, string) error {
	return nil
}

func (f *prepareVoiceFundsTransfererFake) GetSpendBalance(context.Context, uuid.UUID) (decimal.Decimal, error) {
	return f.spend, nil
}

func (f *prepareVoiceFundsTransfererFake) GetStashBalance(context.Context, uuid.UUID) (decimal.Decimal, error) {
	return f.stash, nil
}

func TestPrepareVoiceActionValidTransfer(t *testing.T) {
	userID := uuid.New()
	convID := uuid.New()
	store := newInMemoryPendingActions()
	o := &Orchestrator{
		fundsTransferer: &prepareVoiceFundsTransfererFake{
			spend: decimal.NewFromInt(1000),
			stash: decimal.NewFromInt(1000),
		},
		pending: store,
		logger:  zap.NewNop(),
	}

	action, err := o.PrepareVoiceAction(context.Background(), userID, convID, ToolTransferFunds, map[string]interface{}{
		"from":   "spend",
		"to":     "stash",
		"amount": float64(50),
	})

	require.NoError(t, err)
	require.NotNil(t, action)
	require.Equal(t, ToolTransferFunds, action.Action)
	require.Equal(t, convID, action.ConversationID)

	// Pending action is stored under convID so /confirm can resolve it.
	stored := store.Get(context.Background(), convID)
	require.NotNil(t, stored)
	require.Equal(t, action.ID, stored.ID)
}

func TestPrepareVoiceActionBlocksWithdrawal(t *testing.T) {
	o := &Orchestrator{pending: newInMemoryPendingActions(), logger: zap.NewNop()}

	action, err := o.PrepareVoiceAction(context.Background(), uuid.New(), uuid.New(), ToolInitiateWithdrawal, map[string]interface{}{
		"amount": float64(25),
	})

	require.Error(t, err)
	require.Nil(t, action)
	require.Contains(t, err.Error(), "withdrawals need app confirmation")
}

func TestPrepareVoiceActionRejectsNonVoiceAction(t *testing.T) {
	o := &Orchestrator{pending: newInMemoryPendingActions(), logger: zap.NewNop()}

	action, err := o.PrepareVoiceAction(context.Background(), uuid.New(), uuid.New(), "definitely_not_a_voice_action", map[string]interface{}{})

	require.Error(t, err)
	require.Nil(t, action)
	require.Contains(t, err.Error(), "not available through voice")
}

package revenue_sweep

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

type mockTransfer struct {
	calls []transferCall
	err   error
}

type transferCall struct {
	UserID uuid.UUID
	Amount decimal.Decimal
}

func (m *mockTransfer) TransferToTreasury(_ context.Context, userID uuid.UUID, amount decimal.Decimal, _ string) error {
	m.calls = append(m.calls, transferCall{UserID: userID, Amount: amount})
	return m.err
}

func TestSweepNoDBReturnsNil(t *testing.T) {
	transfer := &mockTransfer{}
	w := NewWorker(transfer, nil, decimal.NewFromFloat(0.10), time.Hour, zap.NewNop())
	err := w.sweep(context.Background())

	assert.NoError(t, err)
	assert.Empty(t, transfer.calls)
}

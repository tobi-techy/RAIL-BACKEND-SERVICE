package station

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledger "github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type fakeLedgerService struct {
	balance decimal.Decimal
	err     error
}

func (f fakeLedgerService) GetAccountBalance(context.Context, uuid.UUID, entities.AccountType) (decimal.Decimal, error) {
	return f.balance, f.err
}

func TestGetAccountBalanceOrZeroTreatsLedgerAccountNotFoundAsZero(t *testing.T) {
	svc := &Service{
		ledgerService: fakeLedgerService{err: fmt.Errorf("lookup failed: %w", ledger.ErrAccountNotFound)},
		logger:        zap.NewNop(),
	}

	got := svc.getAccountBalanceOrZero(context.Background(), uuid.New(), entities.AccountTypeFiatExposure, "Failed to get fiat exposure")

	if !got.Equal(decimal.Zero) {
		t.Fatalf("expected zero balance, got %s", got)
	}
}

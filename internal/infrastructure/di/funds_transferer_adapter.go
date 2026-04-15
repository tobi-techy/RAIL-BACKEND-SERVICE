package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/shopspring/decimal"
)

// fundsTransfererAdapter adapts the ledger service to the FundsTransferer interface.
type fundsTransfererAdapter struct {
	ledger *ledger.Service
}

func (a *fundsTransfererAdapter) TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	key := fmt.Sprintf("ada-spend-to-stash-%s-%d", userID, time.Now().UnixMilli())
	return a.ledger.TransferSpendingToStash(ctx, userID, amount, key)
}

func (a *fundsTransfererAdapter) TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	key := fmt.Sprintf("ada-stash-to-spend-%s-%d", userID, time.Now().UnixMilli())
	return a.ledger.TransferStashToSpending(ctx, userID, amount, key)
}

func (a *fundsTransfererAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

func (a *fundsTransfererAdapter) GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

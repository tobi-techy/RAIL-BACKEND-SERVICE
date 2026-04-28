package di

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

// fundsTransfererAdapter adapts the ledger service to the FundsTransferer interface.
type fundsTransfererAdapter struct {
	ledger *ledger.Service
}

func (a *fundsTransfererAdapter) TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledger.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

func (a *fundsTransfererAdapter) TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledger.TransferStashToSpending(ctx, userID, amount, idempotencyKey)
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

// userProfileAdapter adapts UserRepository to the UserProfileProvider interface.
type userProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *userProfileAdapter) GetCountry(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if user.Country != nil {
		return *user.Country, nil
	}
	return "", nil
}

func (a *userProfileAdapter) GetEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Email, nil
}

// accountCheckerAdapter adapts UserRepository to the UserAccountChecker interface.
type accountCheckerAdapter struct {
	repo *repositories.UserRepository
}

func (a *accountCheckerAdapter) IsActiveAndUnfrozen(ctx context.Context, userID uuid.UUID) (bool, bool, error) {
	user, err := a.repo.GetByID(ctx, userID)
	if err != nil {
		return false, false, err
	}
	return user.IsActive, user.WithdrawalsFrozen, nil
}
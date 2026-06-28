package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	blend "github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// fundsTransfererAdapter adapts the ledger service to the FundsTransferer interface.
type fundsTransfererAdapter struct {
	ledger        *ledger.Service
	blendRouter   *blend.DepositRouter
	logger        *zap.Logger
}

func (a *fundsTransfererAdapter) TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledger.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

func (a *fundsTransfererAdapter) TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if err := a.ledger.TransferStashToSpending(ctx, userID, amount, idempotencyKey); err != nil {
		return err
	}
	// Async: redeem from Blend so on-chain state reconciles with ledger.
	// Non-blocking — the user already has their spending balance.
	if a.blendRouter != nil {
		go func() {
			redeemCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			redeemKey := "redeem-" + idempotencyKey
			if err := a.blendRouter.RedeemStashYield(redeemCtx, userID, amount, redeemKey); err != nil {
				if a.logger != nil {
					a.logger.Warn("async Blend redemption failed (will retry via worker)",
						zap.String("user_id", userID.String()),
						zap.String("amount", amount.StringFixed(6)),
						zap.Error(err))
				}
			}
		}()
	}
	return nil
}

func (a *fundsTransfererAdapter) TransferBetweenStashes(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	idempotencyKey := "automation-" + userID.String() + "-" + from + "-" + to + "-" + amount.String()
	switch {
	case from == "spend" && to == "stash":
		return a.ledger.AutomationTransferSpendToStash(ctx, userID, amount, idempotencyKey, "Scheduled transfer")
	case from == "stash" && to == "spend":
		return a.TransferStashToSpend(ctx, userID, amount, idempotencyKey)
	default:
		return fmt.Errorf("unsupported transfer route: %s to %s", from, to)
	}
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
	if user.AddressCountry != nil {
		return *user.AddressCountry, nil
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

func (a *userProfileAdapter) GetProfile(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error) {
	return a.userRepo.GetByID(ctx, userID)
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

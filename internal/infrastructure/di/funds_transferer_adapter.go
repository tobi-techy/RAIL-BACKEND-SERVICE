package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	blend "github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// fundsTransfererAdapter adapts the ledger service to the FundsTransferer interface.
type fundsTransfererAdapter struct {
	ledger      *ledger.Service
	blendRouter *blend.DepositRouter
	logger      *zap.Logger
}

func (a *fundsTransfererAdapter) TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledger.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

func (a *fundsTransfererAdapter) TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	// Reserve the Blend redemption BEFORE the ledger moves. The reservation is
	// the durable record the reconciliation worker resumes from — taking it
	// first means a crash (or a failed async attempt) after the ledger debit
	// can never leave the ledger ahead of Blend custody with nothing to retry.
	redeemKey := "redeem-" + idempotencyKey
	blendReserved := false
	if a.blendRouter != nil {
		reserved, err := a.blendRouter.EnsureRedemptionReserved(ctx, userID, amount, redeemKey)
		if err != nil {
			// Refuse to move the ledger without a durable redemption record —
			// this is exactly the divergence that stranded funds in Blend.
			return fmt.Errorf("reserve yield redemption: %w", err)
		}
		blendReserved = reserved
	}

	if err := a.ledger.TransferStashToSpending(ctx, userID, amount, idempotencyKey); err != nil {
		if blendReserved {
			// Ledger never moved — the reservation must not be driven.
			abandonCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if aerr := a.blendRouter.AbandonRedemption(abandonCtx, redeemKey, "ledger transfer failed: "+err.Error()); aerr != nil && a.logger != nil {
				a.logger.Error("failed to abandon Blend redemption after ledger failure",
					zap.String("user_id", userID.String()), zap.String("key", redeemKey), zap.Error(aerr))
			}
		}
		return err
	}

	// Async: drive the reserved redemption so on-chain state reconciles with the
	// ledger. Non-blocking — the user already has their spending balance and the
	// reconciliation worker resumes the reserved row if this attempt dies.
	if blendReserved {
		go func() {
			redeemCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := a.blendRouter.RedeemStashYield(redeemCtx, userID, amount, redeemKey); err != nil {
				if a.logger != nil {
					a.logger.Warn("async Blend redemption incomplete (reserved; worker will resume)",
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

// sharedGoalUserLookupAdapter resolves rail tags to user IDs for shared-goal
// invites. Without it the shared-goal service's invite path would dereference
// a nil UserLookup.
type sharedGoalUserLookupAdapter struct {
	repo *repositories.UserRepository
}

func (a *sharedGoalUserLookupAdapter) GetUserIDByRailTag(ctx context.Context, tag string) (uuid.UUID, error) {
	if a.repo == nil {
		return uuid.Nil, fmt.Errorf("user repository not configured for rail-tag lookup")
	}
	user, err := a.repo.GetByRailTag(ctx, tag)
	if err != nil {
		return uuid.Nil, err
	}
	if user == nil {
		return uuid.Nil, fmt.Errorf("no user with rail tag %q", tag)
	}
	return user.ID, nil
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

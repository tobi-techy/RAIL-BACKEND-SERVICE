package vault

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// OnDepositAllocated is the deposit hook the allocation service fires after it
// has split an incoming payment into spend/stash. If the user has an active
// retirement vault with an automatic-saving rule, a share of the deposit is
// routed into it, with no extra taps.
//
// Best-effort by contract: it must never break the deposit path. Failures are
// logged and returned to the caller only for tests.
func (s *Service) OnDepositAllocated(ctx context.Context, userID, depositID uuid.UUID, depositAmount, stashAllocated decimal.Decimal) {
	if !s.cfg.Enabled || depositID == uuid.Nil || !depositAmount.GreaterThan(decimal.Zero) {
		return
	}
	if _, err := s.ContributeFromDeposit(ctx, userID, depositID, depositAmount, stashAllocated); err != nil {
		s.log.Warn("retirement vault auto-route failed",
			"user_id", userID.String(),
			"deposit_id", depositID.String(),
			"error", err)
	}
}

// ContributeFromDeposit computes and funds the automatic contribution for one
// deposit. The percentage is applied to the deposit and clamped to what is
// actually available to save, so a large rule can never overdraw a user.
//
// The spendable floor is checked first: if the take would leave spendable under
// MinSpendableUSD, the take is skipped and a VaultSkip is written instead of a
// lot. The boundary is inclusive — landing exactly on the floor is allowed.
func (s *Service) ContributeFromDeposit(ctx context.Context, userID, depositID uuid.UUID, depositAmount, stashAllocated decimal.Decimal) (*entities.VaultContributionResult, error) {
	if !s.ready() {
		return nil, nil
	}
	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil || vault.GliderEnrollmentID == nil || !vault.AutoContributionPct.GreaterThan(decimal.Zero) {
		return nil, nil
	}

	amount := vault.AutoContributionPct.Mul(depositAmount)
	spendable := decimal.Zero
	haveBalances := false
	if s.ledger != nil {
		balances, err := s.ledger.GetUserBalances(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load balances: %w", err)
		}
		if balances != nil {
			haveBalances = true
			spendable = balances.SpendingBalance
			if amount.GreaterThan(balances.SpendingBalance) {
				amount = balances.SpendingBalance
			}
		}
	}
	if !amount.GreaterThan(decimal.Zero) || amount.LessThan(s.cfg.MinContributionUSD) {
		return nil, nil
	}
	if haveBalances && spendable.Sub(amount).LessThan(s.cfg.MinSpendableUSD) {
		// Same payment_id must not open two skips: a retry of the deposit hook
		// after a partial failure must be a no-op, not a duplicate skip.
		if existing, err := s.repo.FindSkipByPayment(ctx, depositID); err != nil {
			return nil, fmt.Errorf("check existing skip: %w", err)
		} else if existing != nil {
			return nil, nil
		}
		skip := &entities.VaultSkip{
			VaultID:   vault.ID,
			UserID:    userID,
			PaymentID: depositID,
			Reason:    entities.VaultSkipFloor,
			WouldHave: amount,
			Spendable: spendable,
			Floor:     s.cfg.MinSpendableUSD,
		}
		if err := s.repo.CreateSkip(ctx, skip); err != nil {
			return nil, fmt.Errorf("record vault skip: %w", err)
		}
		if err := s.refreshHealth(ctx, vault, "floor skip"); err != nil {
			s.log.Warn("vault health refresh failed", "vault_id", vault.ID.String(), "error", err)
		}
		s.log.Info("retirement vault contribution skipped on floor",
			"vault_id", vault.ID.String(),
			"user_id", userID.String(),
			"would_have_been_usd", amount.String())
		return nil, nil
	}
	if existing, err := s.repo.GetLotByKey(ctx, "vault:auto:"+depositID.String()); err != nil {
		return nil, err
	} else if existing != nil {
		return &entities.VaultContributionResult{
			LotID:      existing.ID,
			AmountUSD:  existing.AmountUSD,
			FundedAt:   existing.AcquiredAt,
			UnlockDate: vault.UnlockDate,
		}, nil
	}
	return s.Contribute(ctx, userID, vault, amount, s.cfg.ContributionSource, "vault:auto:"+depositID.String())
}

// Contribute funds a vault and records the cost-basis lot. It is idempotent on
// the key and fails closed: no money moves, no lot is written, unless the
// funding leg succeeds.
func (s *Service) Contribute(ctx context.Context, userID uuid.UUID, vault *entities.RetirementVault, amount decimal.Decimal, source, idempotencyKey string) (*entities.VaultContributionResult, error) {
	if !s.ready() {
		return nil, ErrDisabled
	}
	if vault == nil || vault.Status != entities.VaultStatusActive {
		return nil, ErrNotFound
	}
	if !amount.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: contribution must be greater than zero", ErrValidation)
	}
	if vault.GliderEnrollmentID == nil {
		return nil, fmt.Errorf("%w: this plan has no portfolio yet", ErrValidation)
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return nil, fmt.Errorf("%w: a contribution needs an idempotency key", ErrValidation)
	}

	if existing, err := s.repo.GetLotByKey(ctx, key); err != nil {
		return nil, err
	} else if existing != nil {
		return &entities.VaultContributionResult{
			LotID:      existing.ID,
			AmountUSD:  existing.AmountUSD,
			FundedAt:   existing.AcquiredAt,
			UnlockDate: vault.UnlockDate,
		}, nil
	}

	transfer, err := s.engine.Fund(ctx, userID, *vault.GliderEnrollmentID, amount, source, key+":fund", entities.InvestmentActorUser)
	if err != nil {
		// Fail closed: the money did not move, so no lot is recorded.
		return nil, fmt.Errorf("fund retirement contribution: %w", err)
	}

	now := s.now()
	lot := &entities.VaultContributionLot{
		ID:             uuid.New(),
		VaultID:        vault.ID,
		UserID:         userID,
		AmountUSD:      amount,
		RemainingUSD:   amount,
		SourceAccount:  source,
		AcquiredAt:     now,
		Status:         entities.VaultLotOpen,
		IdempotencyKey: key,
		CreatedAt:      now,
	}
	if transfer != nil {
		transferID := transfer.ID
		lot.FundingTransferID = &transferID
		lot.LedgerTransactionID = transfer.LedgerTransactionID
	}
	if err := s.repo.CreateLot(ctx, lot); err != nil {
		return nil, err
	}

	// The first contribution anchors the minimum-lock leg of the unlock policy.
	if vault.FundedAt == nil {
		vault.FundedAt = &now
		if err := s.recomputeUnlock(ctx, vault); err != nil {
			return nil, err
		}
		if vault.UnlockDate == nil {
			// We could not resolve the retirement-age leg (no date of birth).
			// The vault stays locked and the user is asked to complete their
			// profile rather than being given a guessed date.
			s.log.Warn("vault funded without a resolvable unlock date",
				"vault_id", vault.ID.String(), "user_id", userID.String())
		}
		if err := s.repo.UpdateVault(ctx, vault); err != nil {
			return nil, err
		}
	}

	if s.notifier != nil {
		if err := s.notifier.NotifyVaultContribution(ctx, userID, amount, vault.UnlockDate); err != nil {
			s.log.Warn("vault contribution notification failed", "vault_id", vault.ID.String(), "error", err)
		}
	}
	if err := s.refreshHealth(ctx, vault, "contribution"); err != nil {
		s.log.Warn("vault health refresh failed", "vault_id", vault.ID.String(), "error", err)
	}

	s.log.Info("retirement vault contribution funded",
		"vault_id", vault.ID.String(),
		"user_id", userID.String(),
		"amount_usd", amount.String(),
		"source", source)

	return &entities.VaultContributionResult{
		LotID:      lot.ID,
		AmountUSD:  amount,
		FundedAt:   now,
		UnlockDate: vault.UnlockDate,
	}, nil
}

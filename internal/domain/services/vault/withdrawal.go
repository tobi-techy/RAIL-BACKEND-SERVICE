package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// PreviewWithdrawal computes the principal/earnings split, the penalty and the
// net payout for a proposed withdrawal, without moving anything.
func (s *Service) PreviewWithdrawal(ctx context.Context, userID uuid.UUID, gross decimal.Decimal) (*entities.VaultWithdrawalPlan, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, ErrNotFound
	}
	position, _, _, _, err := s.position(ctx, vault)
	if err != nil {
		return nil, err
	}
	plan, err := ComputeWithdrawal(position, gross, s.now(), s.cfg.PenaltyRate)
	if err != nil {
		return nil, err
	}
	plan.VaultID = vault.ID
	if vault.GliderEnrollmentID != nil {
		plan.EnrollmentID = *vault.GliderEnrollmentID
	}
	return plan, nil
}

// Withdraw takes money out of the retirement plan.
//
// This is the only path out of a vault. It computes the penalty, mints a
// single-use authorization and hands it to the investment engine, whose gate
// refuses any vault withdrawal that does not carry it. The money is sent by the
// provider to Rail's settlement account; the ledger split happens at settlement.
func (s *Service) Withdraw(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.VaultWithdrawRequest,
	stepUpVerified bool,
) (*entities.VaultWithdrawalResult, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if !stepUpVerified {
		return nil, ErrStepUpRequired
	}
	if req == nil || !req.AmountUSD.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: enter an amount to withdraw", ErrValidation)
	}
	if strings.TrimSpace(s.cfg.SettlementAccount) == "" {
		// Without a Rail-controlled settlement account we cannot retain the
		// penalty, so we refuse rather than let a raided withdrawal through.
		return nil, fmt.Errorf("%w: withdrawals are temporarily unavailable", ErrValidation)
	}

	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, ErrNotFound
	}
	if vault.GliderEnrollmentID == nil {
		return nil, fmt.Errorf("%w: this plan has no portfolio yet", ErrValidation)
	}

	now := s.now()
	inflight, err := s.repo.HasIssuedAuthorization(ctx, *vault.GliderEnrollmentID, now)
	if err != nil {
		return nil, err
	}
	if inflight {
		return nil, fmt.Errorf("%w: a withdrawal from this plan is already in progress", ErrValidation)
	}

	strategy, err := s.strategyForTier(ctx, vault.Tier)
	if err != nil {
		return nil, err
	}
	position, _, _, _, err := s.position(ctx, vault)
	if err != nil {
		return nil, err
	}
	plan, err := ComputeWithdrawal(position, req.AmountUSD, now, s.cfg.PenaltyRate)
	if err != nil {
		return nil, err
	}
	destType, err := destinationAccountType(req.DestinationAccount)
	if err != nil {
		return nil, err
	}
	plan.VaultID = vault.ID
	plan.EnrollmentID = *vault.GliderEnrollmentID
	plan.SettlementAccount = s.cfg.SettlementAccount
	plan.DestinationAccount = destinationName(destType)

	penalty := &entities.VaultPenaltyEvent{
		VaultID:              vault.ID,
		UserID:               userID,
		EarningsWithdrawnUSD: plan.EarningsReturnedUSD,
		PenaltyUSD:           plan.PenaltyUSD,
		Rate:                 s.cfg.PenaltyRate,
		Status:               entities.VaultPenaltyPending,
		Reason:               penaltyReason(plan),
	}
	if err := s.repo.CreatePenaltyEvent(ctx, penalty); err != nil {
		return nil, err
	}
	plan.PenaltyEventID = penalty.ID

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode vault withdrawal plan: %w", err)
	}
	key := uuid.NewString()
	auth := &entities.VaultWithdrawalAuthorization{
		VaultID:      vault.ID,
		UserID:       userID,
		EnrollmentID: *vault.GliderEnrollmentID,
		Key:          key,
		GrossUSD:     plan.GrossUSD,
		PenaltyUSD:   plan.PenaltyUSD,
		NetUSD:       plan.NetToUserUSD,
		Plan:         planJSON,
		Status:       entities.VaultAuthorizationIssued,
		ExpiresAt:    now.Add(s.cfg.AuthorizationTTL),
	}
	if err := s.repo.CreateAuthorization(ctx, auth); err != nil {
		return nil, err
	}

	resp, err := s.engine.Withdraw(ctx, userID, &entities.InvestmentWithdrawalRequest{
		StrategyID:            strategy.ID.String(),
		AmountUSD:             plan.GrossUSD,
		IdempotencyKey:        "vault-withdraw:" + key,
		VaultAuthorizationKey: key,
	}, true, entities.InvestmentActorUser)
	if err != nil {
		// The withdrawal did not go through. Release the authorization and void
		// the penalty so the user is not locked out and no phantom fee remains.
		if expireErr := s.repo.ExpireAuthorization(ctx, key, s.now()); expireErr != nil {
			s.log.Warn("failed to release vault authorization", "key", key, "error", expireErr)
		}
		penalty.Status = entities.VaultPenaltyVoid
		penalty.Reason = err.Error()
		if updateErr := s.repo.UpdatePenaltyEvent(ctx, penalty); updateErr != nil {
			s.log.Warn("failed to void vault penalty event", "penalty_id", penalty.ID.String(), "error", updateErr)
		}
		return nil, err
	}

	result := &entities.VaultWithdrawalResult{Plan: plan, Status: "SUBMITTED"}
	if resp != nil {
		result.OperationID = resp.OperationID
		if resp.Execution != nil {
			execID := resp.Execution.ID
			result.ExecutionID = &execID
			penalty.ExecutionID = &execID
			if err := s.repo.UpdatePenaltyEvent(ctx, penalty); err != nil {
				s.log.Warn("failed to bind vault penalty to execution", "penalty_id", penalty.ID.String(), "error", err)
			}
			if err := s.repo.AttachAuthorizationExecution(ctx, key, execID); err != nil {
				s.log.Warn("failed to attach vault authorization to execution", "key", key, "error", err)
			}
		}
	}

	s.log.Info("retirement vault withdrawal submitted",
		"vault_id", vault.ID.String(),
		"user_id", userID.String(),
		"gross_usd", plan.GrossUSD.String(),
		"penalty_usd", plan.PenaltyUSD.String(),
		"locked", plan.Locked)
	return result, nil
}

// settle posts the ledger split for a filled vault withdrawal: the gross leaves
// the settlement buffer, the user receives the net and Rail keeps the penalty.
// It then consumes the cost basis FIFO and commits the penalty event.
func (s *Service) settle(
	ctx context.Context,
	execution *entities.InvestmentExecution,
	penalty *entities.VaultPenaltyEvent,
	auth *entities.VaultWithdrawalAuthorization,
	plan *entities.VaultWithdrawalPlan,
) error {
	if s.ledger == nil {
		return fmt.Errorf("vault settlement: ledger is not configured")
	}
	destType, err := destinationAccountType(plan.DestinationAccount)
	if err != nil {
		return err
	}
	settlementAccount, err := s.ledger.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("load settlement account: %w", err)
	}
	userAccount, err := s.ledger.GetOrCreateUserAccount(ctx, auth.UserID, destType)
	if err != nil {
		return fmt.Errorf("load user destination account: %w", err)
	}

	// In this ledger a debit increases a balance and a credit decreases it.
	entries := []entities.CreateEntryRequest{
		{
			AccountID: settlementAccount.ID,
			EntryType: entities.EntryTypeCredit, // settlement buffer releases the gross
			Amount:    auth.GrossUSD,
			Currency:  "USDC",
		},
		{
			AccountID: userAccount.ID,
			EntryType: entities.EntryTypeDebit, // the user receives the net
			Amount:    auth.NetUSD,
			Currency:  "USDC",
		},
	}
	if auth.PenaltyUSD.GreaterThan(decimal.Zero) {
		revenueAccount, err := s.ledger.GetSystemAccount(ctx, entities.AccountTypeEarlyRetirementPenalty)
		if err != nil {
			return fmt.Errorf("load penalty revenue account: %w", err)
		}
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: revenueAccount.ID,
			EntryType: entities.EntryTypeDebit, // Rail keeps the haircut
			Amount:    auth.PenaltyUSD,
			Currency:  "USDC",
		})
	}

	description := "Retirement plan withdrawal"
	referenceType := "vault_withdrawal"
	transaction, err := s.ledger.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &auth.UserID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceID:     &execution.ID,
		ReferenceType:   &referenceType,
		IdempotencyKey:  "vault:withdraw:settle:" + execution.ID.String(),
		Description:     &description,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries:         entries,
	})
	if err != nil {
		return fmt.Errorf("settle vault withdrawal: %w", err)
	}

	// Consume cost basis FIFO by the principal returned. The books and the lots
	// must agree; if they do not, we stop rather than improvise.
	lots, err := s.repo.ListOpenLots(ctx, auth.VaultID)
	if err != nil {
		return err
	}
	SortLotsFIFO(lots)
	if err := ConsumeLots(lots, plan.PrincipalReturnedUSD); err != nil {
		return err
	}
	for _, lot := range lots {
		if lot == nil {
			continue
		}
		if err := s.repo.UpdateLot(ctx, lot); err != nil {
			return fmt.Errorf("update vault lot: %w", err)
		}
	}

	penalty.Status = entities.VaultPenaltyCommitted
	penalty.ExecutionID = &execution.ID
	if transaction != nil {
		txID := transaction.ID
		penalty.LedgerTransactionID = &txID
	}
	if err := s.repo.UpdatePenaltyEvent(ctx, penalty); err != nil {
		return err
	}

	if s.notifier != nil {
		if err := s.notifier.NotifyVaultWithdrawal(ctx, auth.UserID, auth.NetUSD, auth.PenaltyUSD, plan.Locked); err != nil {
			s.log.Warn("vault withdrawal notification failed", "vault_id", auth.VaultID.String(), "error", err)
		}
	}
	s.log.Info("retirement vault withdrawal settled",
		"vault_id", auth.VaultID.String(),
		"user_id", auth.UserID.String(),
		"net_usd", auth.NetUSD.String(),
		"penalty_usd", auth.PenaltyUSD.String())
	return nil
}

func destinationName(accountType entities.AccountType) string {
	if accountType == entities.AccountTypeSpendingBalance {
		return "spending"
	}
	return "stash"
}

func penaltyReason(plan *entities.VaultWithdrawalPlan) string {
	if plan == nil || !plan.PenaltyUSD.GreaterThan(decimal.Zero) {
		return "no penalty"
	}
	return fmt.Sprintf("early withdrawal of %.2f in growth at %s", plan.EarningsReturnedUSD.InexactFloat64(), plan.PenaltyRate.String())
}

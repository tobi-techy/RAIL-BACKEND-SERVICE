package withdrawal_recovery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type LedgerReverser interface {
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// LedgerStatusChecker resolves the status of a ledger transaction by its
// idempotency key. Used by the recovery worker to avoid reversing when the
// ledger is still pending (no balance impact) vs already committed (needs
// compensating entries).
type LedgerStatusChecker interface {
	GetLedgerTransactionStatus(ctx context.Context, idempotencyKey string) (entities.TransactionStatus, error)
}

// WithdrawalSyncer polls the upstream provider (Circle/Bridge) for a single
// stuck withdrawal and finalizes it (settle on success, reverse on failure).
// Optional — when nil, the worker only handles pre-transfer stuck cases.
type WithdrawalSyncer interface {
	SyncStuckWithdrawal(ctx context.Context, withdrawalID uuid.UUID) error
}

// Worker periodically finds crypto withdrawals stuck in processing/initiated
// status and either reverses the ledger debit (if the provider transfer never
// started) or polls the provider to finalize it (if the transfer did start
// but the webhook never arrived).
type Worker struct {
	db                  *sql.DB
	ledger              LedgerReverser
	ledgerStatus        LedgerStatusChecker
	syncer              WithdrawalSyncer
	logger              *zap.Logger
	checkInterval       time.Duration
	maxStuckAge         time.Duration
	postTransferSyncAge time.Duration
	stopCh              chan struct{}
}

func NewWorker(db *sql.DB, ledger LedgerReverser, logger *zap.Logger) *Worker {
	if ledger == nil {
		panic("NewWorker: ledger reverser cannot be nil")
	}
	return &Worker{
		db:                  db,
		ledger:              ledger,
		logger:              logger,
		checkInterval:       5 * time.Minute,
		maxStuckAge:         30 * time.Minute,
		postTransferSyncAge: 5 * time.Minute,
		stopCh:              make(chan struct{}),
	}
}

// SetWithdrawalSyncer wires the optional provider sync path. Without this,
// withdrawals stuck in processing after the provider transfer was initiated
// can only be finalized via webhook or user-triggered GetWithdrawal reads.
func (w *Worker) SetWithdrawalSyncer(s WithdrawalSyncer) { w.syncer = s }

// SetLedgerStatusChecker wires the ledger status checker so the recovery
// worker can distinguish pending (no balance impact) from committed (needs
// reversal) ledger entries before reversing.
func (w *Worker) SetLedgerStatusChecker(c LedgerStatusChecker) { w.ledgerStatus = c }

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting withdrawal recovery worker",
		zap.Duration("interval", w.checkInterval),
		zap.Duration("max_stuck_age", w.maxStuckAge))

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.recover(ctx)
		}
	}
}

func (w *Worker) Stop() { close(w.stopCh) }

func (w *Worker) recover(ctx context.Context) {
	w.recoverPreTransferStuck(ctx)
	w.syncPostTransferStuck(ctx)
	w.failChainRailsExpired(ctx)
}

// recoverPreTransferStuck reverses withdrawals where the provider transfer
// never started — these can't possibly land on-chain.
func (w *Worker) recoverPreTransferStuck(ctx context.Context) {
	maxAgeSeconds := int(w.maxStuckAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, amount, fee_amount, source_account, status, updated_at
		FROM withdrawals
		WHERE status IN ('initiated', 'processing')
		  AND withdrawal_type = 'crypto'
		  AND (bridge_transfer_id IS NULL OR bridge_transfer_id = '')
		  AND updated_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("withdrawal recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuck struct {
		ID            uuid.UUID
		UserID        uuid.UUID
		Amount        decimal.Decimal
		FeeAmount     decimal.Decimal
		SourceAccount string
		Status        string
		UpdatedAt     time.Time
	}

	var items []stuck
	for rows.Next() {
		var s stuck
		if err := rows.Scan(&s.ID, &s.UserID, &s.Amount, &s.FeeAmount, &s.SourceAccount, &s.Status, &s.UpdatedAt); err != nil {
			w.logger.Error("withdrawal recovery: scan failed", zap.Error(err))
			continue
		}
		items = append(items, s)
	}

	for _, s := range items {
		if err := w.recoverStuckWithdrawal(ctx, s.ID, s.UserID, s.Amount.Add(s.FeeAmount), s.SourceAccount, s.Status); err != nil {
			w.logger.Error("withdrawal recovery: reversal failed",
				zap.Error(err), zap.String("withdrawal_id", s.ID.String()))
		}
	}
}

// syncPostTransferStuck polls the provider for withdrawals where the transfer
// was initiated (bridge_transfer_id present) but never reached a terminal
// state — typically because the provider webhook was missed. The syncer
// internally settles on success or reverses on provider-confirmed failure.
func (w *Worker) syncPostTransferStuck(ctx context.Context) {
	if w.syncer == nil {
		return
	}
	syncAgeSeconds := int(w.postTransferSyncAge.Seconds())

	// ChainRails (cr:) withdrawals are pollable only when the transfer ID embeds
	// the intent address as a fourth segment (cr:{intentID}:{txID}:{address}).
	// Legacy 3-segment IDs are webhook-only — syncWithdrawalStatusFromProvider
	// short-circuits on them without updating updated_at, so polling them here
	// would burn cycles forever on the same rows.
	rows, err := w.db.QueryContext(ctx, `
		SELECT id
		FROM withdrawals
		WHERE status IN ('processing', 'awaiting_confirmation', 'onchain_transfer', 'timeout')
		  AND withdrawal_type = 'crypto'
		  AND bridge_transfer_id IS NOT NULL
		  AND bridge_transfer_id <> ''
		  AND (bridge_transfer_id NOT LIKE 'cr:%' OR bridge_transfer_id LIKE 'cr:%:%:%')
		  AND updated_at < NOW() - make_interval(secs => $1)
		ORDER BY updated_at ASC LIMIT 25`, syncAgeSeconds)
	if err != nil {
		w.logger.Error("withdrawal recovery: post-transfer query failed", zap.Error(err))
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			w.logger.Error("withdrawal recovery: post-transfer rows close failed", zap.Error(err))
		}
	}()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			w.logger.Error("withdrawal recovery: post-transfer scan failed", zap.Error(err))
			continue
		}
		ids = append(ids, id)
	}

	for _, id := range ids {
		if err := w.syncer.SyncStuckWithdrawal(ctx, id); err != nil {
			w.logger.Warn("withdrawal recovery: provider sync failed",
				zap.String("withdrawal_id", id.String()), zap.Error(err))
		}
	}
}

func (w *Worker) recoverStuckWithdrawal(ctx context.Context, withdrawalID, userID uuid.UUID, totalAmount decimal.Decimal, sourceAccount, status string) error {
	if status == string(entities.WithdrawalStatusInitiated) {
		_, err := w.db.ExecContext(ctx, `
			UPDATE withdrawals
			SET status = 'failed', error_message = 'auto-failed: stuck before ledger debit', updated_at = NOW()
			WHERE id = $1 AND status = 'initiated'`, withdrawalID)
		return err
	}

	if status != string(entities.WithdrawalStatusProcessing) {
		return nil
	}

	if w.ledger == nil {
		return fmt.Errorf("ledger reverser not configured")
	}

	// Check ledger status before reversing. If the ledger entry is still
	// pending (balances never touched), just mark it failed — no reversal
	// needed. If already completed (balances debited), create compensating
	// entries. This prevents double-debiting when the goroutine panicked
	// before the ledger was committed but after the pending entry was created.
	if w.ledgerStatus != nil {
		key := withdrawalLedgerIdempotencyKey(withdrawalID)
		ledgerStatus, err := w.ledgerStatus.GetLedgerTransactionStatus(ctx, key)
		if err != nil {
			w.logger.Warn("withdrawal recovery: failed to check ledger status, proceeding with reversal",
				zap.Error(err), zap.String("withdrawal_id", withdrawalID.String()))
		} else {
			switch ledgerStatus {
			case entities.TransactionStatusPending:
				// Ledger never committed — balances were never debited.
				// Mark the pending entry failed (no-op on balances) and
				// mark the withdrawal failed. No reversal needed.
				w.logger.Info("withdrawal recovery: ledger still pending — marking failed without reversal",
					zap.String("withdrawal_id", withdrawalID.String()))
				_, _ = w.db.ExecContext(ctx, `
					UPDATE withdrawals
					SET status = 'failed', error_message = 'auto-failed: stuck processing, ledger pending', updated_at = NOW()
					WHERE id = $1 AND status = 'processing'`, withdrawalID)
				return nil
			case entities.TransactionStatusFailed, entities.TransactionStatusReversed:
				// Already cleaned up — no-op.
				w.logger.Info("withdrawal recovery: ledger already settled — skipping",
					zap.String("withdrawal_id", withdrawalID.String()),
					zap.String("ledger_status", string(ledgerStatus)))
				return nil
			// TransactionStatusCompleted falls through to reversal below
			default:
				w.logger.Warn("withdrawal recovery: unknown ledger status — proceeding with reversal",
					zap.String("withdrawal_id", withdrawalID.String()),
					zap.String("ledger_status", string(ledgerStatus)))
			}
		}
	}

	accountType := entities.AccountTypeSpendingBalance
	if sourceAccount == string(entities.WithdrawalSourceStashBalance) {
		accountType = entities.AccountTypeStashBalance
	}

	if err := w.ledger.ReverseTransaction(ctx, userID, accountType, withdrawalID.String(), totalAmount, map[string]interface{}{
		"withdrawal_id":   withdrawalID.String(),
		"reversal_reason": "auto_recovery_stuck_processing",
		"source_account":  sourceAccount,
	}); err != nil {
		return err
	}

	res, err := w.db.ExecContext(ctx, `
		UPDATE withdrawals
		SET status = 'reversed', error_message = 'auto-reversed: stuck in processing without provider transfer', updated_at = NOW()
		WHERE id = $1 AND status = 'processing'`, withdrawalID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil
	}

	w.logger.Info("Withdrawal auto-reversed",
		zap.String("withdrawal_id", withdrawalID.String()),
		zap.String("user_id", userID.String()),
		zap.String("amount", totalAmount.String()))
	return nil
}

func withdrawalLedgerIdempotencyKey(withdrawalID uuid.UUID) string {
	return "withdrawal-ledger-" + withdrawalID.String()
}

// failChainRailsExpired catches ChainRails crypto withdrawals that have been
// stuck in processing for over 1 hour. ChainRails relies on webhooks for
// status updates — if the webhook is missed, these stay pending forever.
// After 1h, we assume the transfer failed and reverse the ledger hold.
func (w *Worker) failChainRailsExpired(ctx context.Context) {
	if w.ledger == nil {
		return
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, amount, fee_amount, source_account
		FROM withdrawals
		WHERE status IN ('processing', 'onchain_transfer')
		  AND withdrawal_type = 'crypto'
		  AND bridge_transfer_id LIKE 'cr:%'
		  AND updated_at < NOW() - interval '1 hour'
		LIMIT 10`)
	if err != nil {
		w.logger.Error("withdrawal recovery: chainrails expired query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuck struct {
		ID            uuid.UUID
		UserID        uuid.UUID
		Amount        decimal.Decimal
		FeeAmount     decimal.Decimal
		SourceAccount string
	}

	var items []stuck
	for rows.Next() {
		var s stuck
		if err := rows.Scan(&s.ID, &s.UserID, &s.Amount, &s.FeeAmount, &s.SourceAccount); err != nil {
			continue
		}
		items = append(items, s)
	}

	for _, s := range items {
		totalAmount := s.Amount.Add(s.FeeAmount)

		// Check ledger status before reversing — same guard as recoverStuckWithdrawal.
		if w.ledgerStatus != nil {
			key := withdrawalLedgerIdempotencyKey(s.ID)
			ledgerStatus, err := w.ledgerStatus.GetLedgerTransactionStatus(ctx, key)
			if err != nil {
				w.logger.Warn("withdrawal recovery: chainrails failed to check ledger status",
					zap.Error(err), zap.String("withdrawal_id", s.ID.String()))
			} else if ledgerStatus == entities.TransactionStatusPending || ledgerStatus == entities.TransactionStatusFailed || ledgerStatus == entities.TransactionStatusReversed {
				w.logger.Info("withdrawal recovery: chainrails ledger not committed — marking failed without reversal",
					zap.String("withdrawal_id", s.ID.String()), zap.String("ledger_status", string(ledgerStatus)))
				_, _ = w.db.ExecContext(ctx, `
					UPDATE withdrawals
					SET status = 'failed', error_message = 'auto-failed: ChainRails timeout, ledger not committed', updated_at = NOW()
					WHERE id = $1 AND status IN ('processing', 'onchain_transfer')`, s.ID)
				continue
			}
		}

		accountType := entities.AccountTypeSpendingBalance
		if s.SourceAccount == string(entities.WithdrawalSourceStashBalance) {
			accountType = entities.AccountTypeStashBalance
		}

		if err := w.ledger.ReverseTransaction(ctx, s.UserID, accountType, s.ID.String(), totalAmount, map[string]interface{}{
			"withdrawal_id":   s.ID.String(),
			"reversal_reason": "chainrails_webhook_timeout",
			"source_account":  s.SourceAccount,
		}); err != nil {
			w.logger.Error("withdrawal recovery: chainrails reversal failed",
				zap.Error(err), zap.String("withdrawal_id", s.ID.String()))
			continue
		}

		_, _ = w.db.ExecContext(ctx, `
			UPDATE withdrawals
			SET status = 'failed', error_message = 'auto-failed: ChainRails webhook timeout (1h)', updated_at = NOW()
			WHERE id = $1 AND status IN ('processing', 'onchain_transfer')`, s.ID)

		w.logger.Warn("ChainRails withdrawal auto-failed after timeout",
			zap.String("withdrawal_id", s.ID.String()),
			zap.String("user_id", s.UserID.String()),
			zap.String("amount", totalAmount.String()))
	}
}

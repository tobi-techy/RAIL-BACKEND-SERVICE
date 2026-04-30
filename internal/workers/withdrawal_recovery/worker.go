package withdrawal_recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Worker periodically finds crypto withdrawals stuck in processing/initiated
// status and reverses the ledger debit if the provider transfer never completed.
type Worker struct {
	db            *sql.DB
	logger        *zap.Logger
	checkInterval time.Duration
	maxStuckAge   time.Duration
	stopCh        chan struct{}
}

func NewWorker(db *sql.DB, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		logger:        logger,
		checkInterval: 5 * time.Minute,
		maxStuckAge:   30 * time.Minute,
		stopCh:        make(chan struct{}),
	}
}

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
	maxAgeSeconds := int(w.maxStuckAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, user_id, amount, fee_amount, source_account, bridge_transfer_id
		FROM withdrawals
		WHERE status IN ('initiated', 'processing')
		  AND withdrawal_type = 'crypto'
		  AND updated_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("withdrawal recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuck struct {
		ID                uuid.UUID
		UserID            uuid.UUID
		Amount            decimal.Decimal
		FeeAmount         decimal.Decimal
		SourceAccount     string
		BridgeTransferID  *string
	}

	var items []stuck
	for rows.Next() {
		var s stuck
		if err := rows.Scan(&s.ID, &s.UserID, &s.Amount, &s.FeeAmount, &s.SourceAccount, &s.BridgeTransferID); err != nil {
			w.logger.Error("withdrawal recovery: scan failed", zap.Error(err))
			continue
		}
		items = append(items, s)
	}

	for _, s := range items {
		// Skip ChainRails withdrawals that have a provider ID — they may still complete via webhook
		if s.BridgeTransferID != nil && len(*s.BridgeTransferID) > 0 {
			w.logger.Info("withdrawal recovery: skipping — has provider transfer, may complete via webhook",
				zap.String("withdrawal_id", s.ID.String()),
				zap.String("bridge_transfer_id", *s.BridgeTransferID))
			continue
		}

		if err := w.reverseStuckWithdrawal(ctx, s.ID, s.UserID, s.Amount.Add(s.FeeAmount), s.SourceAccount); err != nil {
			w.logger.Error("withdrawal recovery: reversal failed",
				zap.Error(err), zap.String("withdrawal_id", s.ID.String()))
		}
	}
}

func (w *Worker) reverseStuckWithdrawal(ctx context.Context, withdrawalID, userID uuid.UUID, totalAmount decimal.Decimal, sourceAccount string) error {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Atomically claim — only reverse if still stuck
	var claimed uuid.UUID
	err = tx.QueryRowContext(ctx, `
		UPDATE withdrawals SET status = 'failed', failure_reason = 'auto-reversed: stuck in processing', updated_at = NOW()
		WHERE id = $1 AND status IN ('initiated', 'processing')
		RETURNING id`, withdrawalID).Scan(&claimed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	accountType := "spending_balance"
	if sourceAccount == "stash_balance" {
		accountType = "stash_balance"
	}

	var userAccountID, systemAccountID uuid.UUID
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE user_id = $1 AND account_type = $2`,
		userID, accountType).Scan(&userAccountID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE account_type = 'system_buffer_usdc' AND user_id IS NULL LIMIT 1`,
	).Scan(&systemAccountID); err != nil {
		return err
	}

	txID := uuid.New()
	idempotencyKey := "withdrawal-auto-reversal-" + withdrawalID.String()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, user_id, transaction_type, idempotency_key, status, description, metadata, created_at)
		VALUES ($1, $2, 'reversal', $3, 'completed', 'Auto-reversal: stuck withdrawal',
			jsonb_build_object('withdrawal_id', $4::text, 'type', 'auto_withdrawal_reversal'), NOW())`,
		txID, userID, idempotencyKey, withdrawalID.String())
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'debit', $4, 'USDC', 'Auto-reversal: stuck withdrawal', NOW())`,
		uuid.New(), txID, userAccountID, totalAmount)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'credit', $4, 'USDC', 'Auto-reversal: stuck withdrawal', NOW())`,
		uuid.New(), txID, systemAccountID, totalAmount)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE ledger_accounts SET balance = balance + $1 WHERE id = $2`,
		totalAmount, userAccountID)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	w.logger.Info("Withdrawal auto-reversed",
		zap.String("withdrawal_id", withdrawalID.String()),
		zap.String("user_id", userID.String()),
		zap.String("amount", totalAmount.String()))
	return nil
}

package paj_offramp_recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// LedgerReverser reverses a ledger hold.
type LedgerReverser interface {
	ReverseHold(ctx context.Context, userID uuid.UUID, pajOrderID string, amount decimal.Decimal, reason string)
}

// Worker periodically finds stuck Paj offramp orders (pending for too long with no
// bridge_transfer_id) and reverses the ledger hold so users aren't stuck with debited balances.
type Worker struct {
	db            *sql.DB
	logger        *zap.Logger
	checkInterval time.Duration
	maxPendingAge time.Duration
	stopCh        chan struct{}
}

func NewWorker(db *sql.DB, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		logger:        logger,
		checkInterval: 2 * time.Minute,
		maxPendingAge: 15 * time.Minute,
		stopCh:        make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting Paj offramp recovery worker",
		zap.Duration("check_interval", w.checkInterval),
		zap.Duration("max_pending_age", w.maxPendingAge))

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

type stuckOrder struct {
	ID         uuid.UUID
	PajOrderID string
	UserID     uuid.UUID
	HoldAmount decimal.Decimal
	FiatAmount float64
	CreatedAt  time.Time
}

func (w *Worker) recover(ctx context.Context) {
	// Find offramp orders that are still pending, have no bridge_transfer_id,
	// and are older than maxPendingAge. These are orders where the Bridge
	// transfer never happened (the bug we fixed).
	rows, err := w.db.QueryContext(ctx, `
		SELECT id, paj_order_id, user_id, COALESCE(hold_amount, token_amount), fiat_amount, created_at
		FROM paj_orders
		WHERE order_type = 'offramp'
		  AND status = 'pending'
		  AND bridge_transfer_id IS NULL
		  AND created_at < NOW() - $1::interval
		LIMIT 10`, w.maxPendingAge.String())
	if err != nil {
		w.logger.Error("paj offramp recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var stuck []stuckOrder
	for rows.Next() {
		var o stuckOrder
		if err := rows.Scan(&o.ID, &o.PajOrderID, &o.UserID, &o.HoldAmount, &o.FiatAmount, &o.CreatedAt); err != nil {
			w.logger.Error("paj offramp recovery: scan failed", zap.Error(err))
			continue
		}
		stuck = append(stuck, o)
	}

	for _, o := range stuck {
		w.reverseStuckOrder(ctx, o)
	}
}

func (w *Worker) reverseStuckOrder(ctx context.Context, o stuckOrder) {
	if o.HoldAmount.IsZero() || o.HoldAmount.IsNegative() {
		return
	}

	// Atomically claim this order for reversal (prevents double-reversal)
	result, err := w.db.ExecContext(ctx, `
		UPDATE paj_orders SET status = 'failed', updated_at = NOW()
		WHERE paj_order_id = $1 AND status = 'pending' AND bridge_transfer_id IS NULL`,
		o.PajOrderID)
	if err != nil {
		w.logger.Error("paj offramp recovery: failed to mark order as failed",
			zap.Error(err), zap.String("paj_order_id", o.PajOrderID))
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return // Already claimed by another process
	}

	// Reverse the ledger hold via direct SQL (same pattern as manual reversal)
	txID := uuid.New()
	desc := "Auto-reversal: stuck Paj offramp (no Bridge transfer)"

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		w.logger.Error("paj offramp recovery: begin tx failed", zap.Error(err))
		return
	}
	defer tx.Rollback()

	// Get user's spending_balance account
	var userAccountID uuid.UUID
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE user_id = $1 AND account_type = 'spending_balance'`,
		o.UserID).Scan(&userAccountID)
	if err != nil {
		w.logger.Error("paj offramp recovery: user account not found",
			zap.Error(err), zap.String("user_id", o.UserID.String()))
		return
	}

	// Get system buffer account
	var systemAccountID uuid.UUID
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE account_type = 'system_buffer_usdc' AND user_id IS NULL LIMIT 1`).Scan(&systemAccountID)
	if err != nil {
		w.logger.Error("paj offramp recovery: system account not found", zap.Error(err))
		return
	}

	// Create reversal transaction
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, user_id, transaction_type, idempotency_key, status, description, metadata, created_at)
		VALUES ($1, $2, 'reversal', $3, 'completed', $4, $5, NOW())`,
		txID, o.UserID,
		"paj-offramp-auto-reversal-"+o.PajOrderID,
		desc,
		`{"provider":"paj","type":"auto_offramp_reversal","paj_order_id":"`+o.PajOrderID+`","fiat_amount":`+decimal.NewFromFloat(o.FiatAmount).String()+`}`)
	if err != nil {
		w.logger.Error("paj offramp recovery: insert tx failed", zap.Error(err))
		return
	}

	// Debit user account (increases balance)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'debit', $4, 'USDC', $5, NOW())`,
		uuid.New(), txID, userAccountID, o.HoldAmount, desc)
	if err != nil {
		w.logger.Error("paj offramp recovery: insert debit entry failed", zap.Error(err))
		return
	}

	// Credit system buffer
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'credit', $4, 'USDC', $5, NOW())`,
		uuid.New(), txID, systemAccountID, o.HoldAmount, desc)
	if err != nil {
		w.logger.Error("paj offramp recovery: insert credit entry failed", zap.Error(err))
		return
	}

	// Update cached balance
	_, err = tx.ExecContext(ctx,
		`UPDATE ledger_accounts SET balance = balance + $1 WHERE id = $2`,
		o.HoldAmount, userAccountID)
	if err != nil {
		w.logger.Error("paj offramp recovery: update balance failed", zap.Error(err))
		return
	}

	if err := tx.Commit(); err != nil {
		w.logger.Error("paj offramp recovery: commit failed", zap.Error(err))
		return
	}

	w.logger.Info("Paj offramp auto-reversed stuck order",
		zap.String("paj_order_id", o.PajOrderID),
		zap.String("user_id", o.UserID.String()),
		zap.String("amount", o.HoldAmount.String()),
		zap.Float64("fiat_amount", o.FiatAmount))
}

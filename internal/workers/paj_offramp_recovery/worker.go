package paj_offramp_recovery

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Worker periodically finds stuck Paj offramp orders (pending too long with no
// bridge_transfer_id) and reverses the ledger hold atomically.
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

func (w *Worker) recover(ctx context.Context) {
	// Use seconds for PostgreSQL interval compatibility.
	maxAgeSeconds := int(w.maxPendingAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, paj_order_id, user_id, COALESCE(hold_amount, token_amount, 0), fiat_amount
		FROM paj_orders
		WHERE order_type = 'offramp'
		  AND status = 'pending'
		  AND bridge_transfer_id IS NULL
		  AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("paj offramp recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type candidate struct {
		ID         uuid.UUID
		PajOrderID string
		UserID     uuid.UUID
		HoldAmount decimal.Decimal
		FiatAmount float64
	}

	var stuck []candidate
	for rows.Next() {
		var o candidate
		if err := rows.Scan(&o.ID, &o.PajOrderID, &o.UserID, &o.HoldAmount, &o.FiatAmount); err != nil {
			w.logger.Error("paj offramp recovery: scan failed", zap.Error(err))
			continue
		}
		if o.HoldAmount.IsZero() || o.HoldAmount.IsNegative() {
			continue
		}
		stuck = append(stuck, o)
	}

	for _, o := range stuck {
		if err := w.reverseStuckOrder(ctx, o.PajOrderID, o.UserID, o.HoldAmount, o.FiatAmount); err != nil {
			w.logger.Error("paj offramp recovery: reversal failed",
				zap.Error(err), zap.String("paj_order_id", o.PajOrderID))
		}
	}
}

// reverseStuckOrder atomically claims the order AND reverses the ledger hold
// in a single database transaction to prevent double-reversal races.
func (w *Worker) reverseStuckOrder(ctx context.Context, pajOrderID string, userID uuid.UUID, holdAmount decimal.Decimal, fiatAmount float64) error {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Atomically claim: set status=failed AND deposit_id (blocks both worker re-entry
	// AND the webhook reverseOfframpIfFailed path which checks deposit_id IS NULL).
	var claimedHold decimal.Decimal
	err = tx.QueryRowContext(ctx, `
		UPDATE paj_orders
		SET status = 'failed', deposit_id = gen_random_uuid(), updated_at = NOW()
		WHERE paj_order_id = $1
		  AND status = 'pending'
		  AND bridge_transfer_id IS NULL
		  AND deposit_id IS NULL
		RETURNING COALESCE(hold_amount, token_amount)`, pajOrderID).Scan(&claimedHold)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // Already claimed by another process — safe no-op
		}
		return fmt.Errorf("claim order: %w", err)
	}

	if claimedHold.IsZero() || claimedHold.IsNegative() {
		return nil
	}

	// Get account IDs within the same transaction.
	var userAccountID, systemAccountID uuid.UUID
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE user_id = $1 AND account_type = 'spending_balance'`,
		userID).Scan(&userAccountID)
	if err != nil {
		return fmt.Errorf("user account lookup: %w", err)
	}

	err = tx.QueryRowContext(ctx,
		`SELECT id FROM ledger_accounts WHERE account_type = 'system_buffer_usdc' AND user_id IS NULL LIMIT 1`,
	).Scan(&systemAccountID)
	if err != nil {
		return fmt.Errorf("system account lookup: %w", err)
	}

	// Create reversal with idempotent key (prevents duplicate reversals on retry).
	txID := uuid.New()
	idempotencyKey := "paj-offramp-auto-reversal-" + pajOrderID
	desc := "Auto-reversal: stuck Paj offramp (no Bridge transfer)"

	// Use parameterized JSON to prevent injection.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, user_id, transaction_type, idempotency_key, status, description, metadata, created_at)
		VALUES ($1, $2, 'reversal', $3, 'completed', $4,
			jsonb_build_object('provider','paj','type','auto_offramp_reversal','paj_order_id',$5::text,'fiat_amount',$6::numeric),
			NOW())`,
		txID, userID, idempotencyKey, desc, pajOrderID, fiatAmount)
	if err != nil {
		return fmt.Errorf("insert reversal tx: %w", err)
	}

	// Debit user account (increases balance).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'debit', $4, 'USDC', $5, NOW())`,
		uuid.New(), txID, userAccountID, claimedHold, desc)
	if err != nil {
		return fmt.Errorf("insert debit entry: %w", err)
	}

	// Credit system buffer.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'credit', $4, 'USDC', $5, NOW())`,
		uuid.New(), txID, systemAccountID, claimedHold, desc)
	if err != nil {
		return fmt.Errorf("insert credit entry: %w", err)
	}

	// Update cached balance atomically.
	_, err = tx.ExecContext(ctx,
		`UPDATE ledger_accounts SET balance = balance + $1 WHERE id = $2`,
		claimedHold, userAccountID)
	if err != nil {
		return fmt.Errorf("update cached balance: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	w.logger.Info("Paj offramp auto-reversed stuck order",
		zap.String("paj_order_id", pajOrderID),
		zap.String("user_id", userID.String()),
		zap.String("amount", claimedHold.String()),
		zap.Float64("fiat_amount", fiatAmount))
	return nil
}

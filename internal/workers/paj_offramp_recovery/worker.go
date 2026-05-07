package paj_offramp_recovery

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

// Worker periodically finds stuck Paj offramp orders (pending too long with no
// bridge_transfer_id) and reverses the ledger hold atomically.
type Worker struct {
	db            *sql.DB
	ledger        LedgerReverser
	logger        *zap.Logger
	checkInterval time.Duration
	maxPendingAge time.Duration
	stopCh        chan struct{}
}

func NewWorker(db *sql.DB, ledger LedgerReverser, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		ledger:        ledger,
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
		SELECT id, paj_order_id, user_id, COALESCE(hold_amount, token_amount, 0), fiat_amount, bridge_transfer_id, created_at
		FROM paj_orders
		WHERE order_type = 'offramp'
		  AND status = 'pending'
		  AND deposit_id IS NULL
		  AND bridge_transfer_id IS NULL
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
		TransferID *string
		CreatedAt  time.Time
	}

	var stuck []candidate
	for rows.Next() {
		var o candidate
		if err := rows.Scan(&o.ID, &o.PajOrderID, &o.UserID, &o.HoldAmount, &o.FiatAmount, &o.TransferID, &o.CreatedAt); err != nil {
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

// reverseStuckOrder claims the order before reversing the ledger hold through
// the shared ledger service. The ledger idempotency key prevents double credits
// if recovery races with a webhook or a retry.
func (w *Worker) reverseStuckOrder(ctx context.Context, pajOrderID string, userID uuid.UUID, holdAmount decimal.Decimal, fiatAmount float64) error {
	// Atomically claim: set status=failed AND deposit_id (blocks both worker re-entry
	// AND the webhook reverseOfframpIfFailed path which checks deposit_id IS NULL).
	var claimedHold decimal.Decimal
	err := w.db.QueryRowContext(ctx, `
		UPDATE paj_orders
		SET status = 'failed', deposit_id = gen_random_uuid(), updated_at = NOW()
		WHERE paj_order_id = $1
		  AND status = 'pending'
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

	if w.ledger == nil {
		_, _ = w.db.ExecContext(ctx, `
			UPDATE paj_orders SET status = 'pending', deposit_id = NULL, updated_at = NOW()
			WHERE paj_order_id = $1 AND status = 'failed'`, pajOrderID)
		return fmt.Errorf("ledger reverser not configured")
	}

	err = w.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"paj-offramp-"+pajOrderID, claimedHold, map[string]interface{}{
			"provider":     "paj",
			"type":         "auto_offramp_reversal",
			"paj_order_id": pajOrderID,
			"fiat_amount":  fiatAmount,
		})
	if err != nil {
		_, _ = w.db.ExecContext(ctx, `
			UPDATE paj_orders SET status = 'pending', deposit_id = NULL, updated_at = NOW()
			WHERE paj_order_id = $1 AND status = 'failed'`, pajOrderID)
		return fmt.Errorf("reverse hold: %w", err)
	}

	w.logger.Info("Paj offramp auto-reversed stuck order",
		zap.String("paj_order_id", pajOrderID),
		zap.String("user_id", userID.String()),
		zap.String("amount", claimedHold.String()),
		zap.Float64("fiat_amount", fiatAmount))
	return nil
}

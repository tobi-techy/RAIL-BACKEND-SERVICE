// Package ramphub_offramp_recovery auto-resolves stuck RampHub offramp orders:
// it reverses holds for orders whose Circle transfer never started, reconciles
// orders whose transfer was initiated but never webhooked (using Circle's own
// state as the source of truth), and force-fails orders past a hard timeout.
// Mirrors the Paj offramp recovery worker.
package ramphub_offramp_recovery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type LedgerReverser interface {
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

type Notifier interface {
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destination string) error
}

type CircleTransferStatus int

const (
	CircleTransferUnknown CircleTransferStatus = iota
	CircleTransferPending
	CircleTransferComplete
	CircleTransferFailed
)

type CircleStatusChecker interface {
	GetCircleTransferStatus(ctx context.Context, circleTxID string) (CircleTransferStatus, error)
}

type Worker struct {
	db               *sql.DB
	ledger           LedgerReverser
	notifier         Notifier
	circle           CircleStatusChecker
	logger           *zap.Logger
	checkInterval    time.Duration
	maxPendingAge    time.Duration
	hardMaxAge       time.Duration
	completeAfterAge time.Duration
	stopCh           chan struct{}
}

func NewWorker(db *sql.DB, ledger LedgerReverser, logger *zap.Logger) *Worker {
	return &Worker{
		db:               db,
		ledger:           ledger,
		logger:           logger,
		checkInterval:    2 * time.Minute,
		maxPendingAge:    15 * time.Minute,
		hardMaxAge:       6 * time.Hour,
		completeAfterAge: 2 * time.Hour,
		stopCh:           make(chan struct{}),
	}
}

func (w *Worker) SetNotifier(n Notifier)                       { w.notifier = n }
func (w *Worker) SetCircleStatusChecker(c CircleStatusChecker) { w.circle = c }

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting RampHub offramp recovery worker",
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
	w.reverseAbandonedOrders(ctx)
	w.reconcileStuckOrders(ctx)
	w.failExpiredOrders(ctx)
}

// reverseAbandonedOrders refunds offramps where the Circle transfer never
// started (no bridge_transfer_id) and the order has been pending too long.
func (w *Worker) reverseAbandonedOrders(ctx context.Context) {
	maxAgeSeconds := int(w.maxPendingAge.Seconds())
	rows, err := w.db.QueryContext(ctx, `
		SELECT ramphub_transaction_id, user_id, COALESCE(hold_amount, token_amount, 0), fiat_amount
		FROM ramphub_orders
		WHERE order_type = 'offramp' AND status = 'pending'
		  AND deposit_id IS NULL AND bridge_transfer_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("ramphub offramp recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type candidate struct {
		TxID       string
		UserID     uuid.UUID
		HoldAmount decimal.Decimal
		FiatAmount float64
	}
	var stuck []candidate
	for rows.Next() {
		var o candidate
		if err := rows.Scan(&o.TxID, &o.UserID, &o.HoldAmount, &o.FiatAmount); err != nil {
			w.logger.Error("ramphub offramp recovery: scan failed", zap.Error(err))
			continue
		}
		if o.HoldAmount.IsPositive() {
			stuck = append(stuck, o)
		}
	}
	for _, o := range stuck {
		if err := w.reverseStuckOrder(ctx, o.TxID, o.UserID, "auto_offramp_reversal"); err != nil {
			w.logger.Error("ramphub offramp recovery: reversal failed", zap.Error(err), zap.String("ramphub_tx_id", o.TxID))
		}
	}
}

type stuckOrder struct {
	TxID             string
	UserID           uuid.UUID
	FiatAmount       float64
	BridgeTransferID string
	HoldAmount       decimal.Decimal
}

// reconcileStuckOrders finalizes offramps whose direct Circle transfer was
// initiated (bridge_transfer_id = "circle:...") but never webhooked, using
// Circle's terminal state. ChainRails (circle-cr:) transfers rely on webhooks.
func (w *Worker) reconcileStuckOrders(ctx context.Context) {
	if w.circle == nil {
		return
	}
	completeAgeSeconds := int(w.completeAfterAge.Seconds())
	rows, err := w.db.QueryContext(ctx, `
		SELECT ramphub_transaction_id, user_id, fiat_amount, bridge_transfer_id, COALESCE(hold_amount, token_amount, 0)
		FROM ramphub_orders
		WHERE order_type = 'offramp' AND status IN ('pending','processing')
		  AND bridge_transfer_id LIKE 'circle:%'
		  AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		ORDER BY created_at ASC LIMIT 25`, completeAgeSeconds)
	if err != nil {
		w.logger.Error("ramphub offramp reconciliation: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var candidates []stuckOrder
	for rows.Next() {
		var c stuckOrder
		if err := rows.Scan(&c.TxID, &c.UserID, &c.FiatAmount, &c.BridgeTransferID, &c.HoldAmount); err != nil {
			w.logger.Error("ramphub offramp reconciliation: scan failed", zap.Error(err))
			continue
		}
		candidates = append(candidates, c)
	}
	for _, c := range candidates {
		if err := w.reconcileStuckOrder(ctx, c); err != nil {
			w.logger.Error("ramphub offramp reconciliation: failed", zap.Error(err), zap.String("ramphub_tx_id", c.TxID))
		}
	}
}

func (w *Worker) reconcileStuckOrder(ctx context.Context, c stuckOrder) error {
	circleTxID := strings.TrimPrefix(c.BridgeTransferID, "circle:")
	if circleTxID == "" {
		return nil
	}
	state, err := w.circle.GetCircleTransferStatus(ctx, circleTxID)
	if err != nil {
		return fmt.Errorf("circle status check: %w", err)
	}
	switch state {
	case CircleTransferComplete:
		return w.promoteCompleted(ctx, c)
	case CircleTransferFailed:
		return w.reverseStuckOrder(ctx, c.TxID, c.UserID, "circle_confirmed_failure_reversal")
	default:
		return nil
	}
}

func (w *Worker) promoteCompleted(ctx context.Context, c stuckOrder) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE ramphub_orders
		SET status = 'completed', updated_at = NOW(), last_webhook_status = 'auto-completed:recovery'
		WHERE ramphub_transaction_id = $1 AND order_type = 'offramp'
		  AND status IN ('pending','processing') AND bridge_transfer_id IS NOT NULL AND deposit_id IS NULL`, c.TxID)
	if err != nil {
		return fmt.Errorf("promote stuck offramp: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil
	}
	w.logger.Info("RampHub offramp auto-completed (Circle COMPLETE)", zap.String("ramphub_tx_id", c.TxID), zap.Float64("fiat_amount", c.FiatAmount))
	if w.notifier != nil {
		_ = w.notifier.NotifyWithdrawalCompleted(ctx, c.UserID, fmt.Sprintf("₦%.0f", c.FiatAmount), "bank account")
	}
	return nil
}

// reverseStuckOrder atomically claims the order and reverses the hold using the
// same idempotency key the service/webhook path uses, preventing double credits.
func (w *Worker) reverseStuckOrder(ctx context.Context, txID string, userID uuid.UUID, reasonType string) error {
	if w.ledger == nil {
		w.logger.Error("CRITICAL: ledger reverser not configured — skipping claim", zap.String("ramphub_tx_id", txID))
		return fmt.Errorf("ledger reverser not configured")
	}
	var claimedHold decimal.Decimal
	var fiatAmount float64
	err := w.db.QueryRowContext(ctx, `
		UPDATE ramphub_orders
		SET status = 'failed', deposit_id = gen_random_uuid(), last_webhook_status = $2, updated_at = NOW()
		WHERE ramphub_transaction_id = $1 AND status IN ('pending','processing') AND deposit_id IS NULL
		RETURNING COALESCE(hold_amount, token_amount, 0), fiat_amount`, txID, "auto-failed:"+reasonType).Scan(&claimedHold, &fiatAmount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("claim order: %w", err)
	}
	if claimedHold.IsZero() || claimedHold.IsNegative() {
		return nil
	}
	if err := w.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		"ramphub-offramp-"+txID, claimedHold, map[string]interface{}{
			"provider": "ramphub", "type": reasonType, "ramphub_tx_id": txID, "fiat_amount": fiatAmount,
		}); err != nil {
		// Unclaim so the next worker sweep can retry the reversal.
		if _, unclaimErr := w.db.ExecContext(context.Background(), `UPDATE ramphub_orders SET deposit_id = NULL, status = 'processing', updated_at = NOW() WHERE ramphub_transaction_id = $1 AND status = 'failed'`, txID); unclaimErr != nil {
			w.logger.Error("CRITICAL: failed to unclaim RampHub order after failed reversal — requires immediate manual intervention",
				zap.Error(unclaimErr), zap.String("user_id", userID.String()),
				zap.String("ramphub_tx_id", txID), zap.String("amount", claimedHold.String()))
		}
		return fmt.Errorf("reverse hold: %w", err)
	}
	w.logger.Info("RampHub offramp auto-reversed", zap.String("ramphub_tx_id", txID), zap.String("amount", claimedHold.String()))
	return nil
}

// failExpiredOrders force-fails offramps still pending past the hard timeout
// ONLY when no transfer was ever initiated (bridge_transfer_id IS NULL). Orders
// whose USDC transfer has started (direct Circle or ChainRails bridge) are left
// for reconciliation/manual review — auto-reversing them would refund a user
// whose crypto already left, a double-spend.
func (w *Worker) failExpiredOrders(ctx context.Context) {
	if w.ledger == nil {
		return
	}
	rows, err := w.db.QueryContext(ctx, `
		SELECT ramphub_transaction_id, user_id
		FROM ramphub_orders
		WHERE order_type = 'offramp' AND status IN ('pending','processing') AND deposit_id IS NULL
		  AND bridge_transfer_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		LIMIT 20`, int(w.hardMaxAge.Seconds()))
	if err != nil {
		w.logger.Error("ramphub offramp hard-timeout: query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type expired struct {
		TxID   string
		UserID uuid.UUID
	}
	var orders []expired
	for rows.Next() {
		var o expired
		if err := rows.Scan(&o.TxID, &o.UserID); err != nil {
			continue
		}
		orders = append(orders, o)
	}
	for _, o := range orders {
		if err := w.reverseStuckOrder(ctx, o.TxID, o.UserID, "hard_timeout_reversal"); err != nil {
			w.logger.Error("ramphub offramp hard-timeout: reversal failed", zap.Error(err), zap.String("ramphub_tx_id", o.TxID))
		}
	}
}

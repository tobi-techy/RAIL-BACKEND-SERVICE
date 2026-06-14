package paj_offramp_recovery

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

// Notifier sends a completion notification to the user when an offramp is auto-finalized.
// Optional — recovery still runs without it.
type Notifier interface {
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destination string) error
}

// CircleTransferStatus is the subset of state we need from Circle to decide
// whether a stuck offramp actually delivered USDC to PAJ.
type CircleTransferStatus int

const (
	CircleTransferUnknown CircleTransferStatus = iota
	CircleTransferPending
	CircleTransferComplete
	CircleTransferFailed
)

// CircleStatusChecker fetches the terminal state of a Circle transaction by ID.
// Used to verify whether USDC actually reached PAJ before auto-completing a
// stuck offramp. Optional — without it, the worker leaves bridge-initiated
// orders alone for the webhook/manual review to resolve.
type CircleStatusChecker interface {
	GetCircleTransferStatus(ctx context.Context, circleTxID string) (CircleTransferStatus, error)
}

// Worker periodically finds stuck Paj offramp orders and either reverses the
// hold (transfer never started), reverses+claims the order (Circle confirmed
// failure), or promotes the order to completed (Circle confirmed success and
// PAJ's NGN-delivery SLA has passed).
type Worker struct {
	db               *sql.DB
	ledger           LedgerReverser
	notifier         Notifier
	circle           CircleStatusChecker
	logger           *zap.Logger
	checkInterval    time.Duration
	maxPendingAge    time.Duration
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
		completeAfterAge: 2 * time.Hour,
		stopCh:           make(chan struct{}),
	}
}

// SetNotifier wires the optional notifier so users are informed when their
// stuck offramp is auto-completed.
func (w *Worker) SetNotifier(n Notifier) { w.notifier = n }

// SetCircleStatusChecker wires Circle state lookup so the recovery can verify
// whether the USDC transfer actually reached PAJ. Without it, completion is
// skipped (we only reverse pre-transfer-stuck orders) — better to leave a
// stuck order than to fabricate a completion.
func (w *Worker) SetCircleStatusChecker(c CircleStatusChecker) { w.circle = c }

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
	w.reverseAbandonedOrders(ctx)
	w.reconcileStuckOrders(ctx)
}

// reverseAbandonedOrders refunds offramps where the Circle/Bridge transfer
// never started (no bridge_transfer_id) and the order has been pending too long.
func (w *Worker) reverseAbandonedOrders(ctx context.Context) {
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

type stuckOrder struct {
	PajOrderID       string
	UserID           uuid.UUID
	FiatAmount       float64
	BridgeTransferID string
	HoldAmount       decimal.Decimal
}

// reconcileStuckOrders finalizes offramps where the USDC transfer was
// initiated (bridge_transfer_id IS NOT NULL) but the webhook never landed.
// Each candidate is verified against Circle: on COMPLETE we promote the order
// (PAJ has had completeAfterAge to deliver NGN); on FAILED/CANCELLED/DENIED we
// reverse the hold. ChainRails (cr:) transfers are skipped — webhooks remain
// the source of truth for those.
func (w *Worker) reconcileStuckOrders(ctx context.Context) {
	if w.circle == nil {
		// Without provider verification we can't safely auto-complete; abort
		// rather than fabricate a status. Operators see the missing wiring in
		// startup logs.
		return
	}

	completeAgeSeconds := int(w.completeAfterAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT paj_order_id, user_id, fiat_amount, bridge_transfer_id,
		       COALESCE(hold_amount, token_amount, 0)
		FROM paj_orders
		WHERE order_type = 'offramp'
		  AND status = 'pending'
		  AND bridge_transfer_id IS NOT NULL
		  AND bridge_transfer_id <> ''
		  AND bridge_transfer_id NOT LIKE 'cr:%'
		  AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		ORDER BY created_at ASC LIMIT 25`, completeAgeSeconds)
	if err != nil {
		w.logger.Error("paj offramp reconciliation: query failed", zap.Error(err))
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			w.logger.Error("paj offramp reconciliation: rows close failed", zap.Error(err))
		}
	}()

	var candidates []stuckOrder
	for rows.Next() {
		var c stuckOrder
		if err := rows.Scan(&c.PajOrderID, &c.UserID, &c.FiatAmount, &c.BridgeTransferID, &c.HoldAmount); err != nil {
			w.logger.Error("paj offramp reconciliation: scan failed", zap.Error(err))
			continue
		}
		candidates = append(candidates, c)
	}

	for _, c := range candidates {
		if err := w.reconcileStuckOrder(ctx, c); err != nil {
			w.logger.Error("paj offramp reconciliation: failed",
				zap.Error(err), zap.String("paj_order_id", c.PajOrderID))
		}
	}
}

// reconcileStuckOrder polls Circle for the actual transfer state and either
// promotes (USDC reached PAJ) or reverses (USDC never left / Circle failed)
// the order. Returns nil for transient states — the next cycle will retry.
func (w *Worker) reconcileStuckOrder(ctx context.Context, c stuckOrder) error {
	// pajfunding stores Circle transfers as "circle:<txID>". The recon query
	// already filters out ChainRails (cr:%), so anything without a circle:
	// prefix here is an unexpected shape we'd rather not guess at.
	if !strings.HasPrefix(c.BridgeTransferID, "circle:") {
		w.logger.Warn("paj offramp reconciliation: unrecognized bridge_transfer_id prefix",
			zap.String("paj_order_id", c.PajOrderID),
			zap.String("bridge_transfer_id", c.BridgeTransferID))
		return nil
	}
	circleTxID := strings.TrimPrefix(c.BridgeTransferID, "circle:")
	if circleTxID == "" {
		return nil
	}

	state, err := w.circle.GetCircleTransferStatus(ctx, circleTxID)
	if err != nil {
		// Provider unreachable — leave the order; next cycle retries.
		return fmt.Errorf("circle status check: %w", err)
	}

	switch state {
	case CircleTransferComplete:
		return w.promoteCompleted(ctx, c)
	case CircleTransferFailed:
		return w.reverseConfirmedFailure(ctx, c)
	default:
		// PENDING / UNKNOWN — leave for the next cycle. The order remains in
		// 'pending' (user sees Processing) until Circle reaches a terminal state.
		return nil
	}
}

// promoteCompleted atomically marks the order completed and writes a
// terminal last_webhook_status that prevents a later webhook from overwriting
// the state via the `last_webhook_status LIKE 'unverified:%'` fallback path.
func (w *Worker) promoteCompleted(ctx context.Context, c stuckOrder) error {
	res, err := w.db.ExecContext(ctx, `
		UPDATE paj_orders
		SET status = 'completed',
		    updated_at = NOW(),
		    last_webhook_status = 'auto-completed:recovery'
		WHERE paj_order_id = $1
		  AND order_type = 'offramp'
		  AND status = 'pending'
		  AND bridge_transfer_id IS NOT NULL
		  AND deposit_id IS NULL`, c.PajOrderID)
	if err != nil {
		return fmt.Errorf("promote stuck offramp: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("promote stuck offramp: rows affected: %w", err)
	}
	if affected == 0 {
		return nil
	}

	w.logger.Info("Paj offramp auto-completed (Circle COMPLETE)",
		zap.String("paj_order_id", c.PajOrderID),
		zap.String("user_id", c.UserID.String()),
		zap.Float64("fiat_amount", c.FiatAmount))

	if w.notifier != nil {
		amountStr := fmt.Sprintf("₦%.0f", c.FiatAmount)
		if err := w.notifier.NotifyWithdrawalCompleted(ctx, c.UserID, amountStr, "bank account"); err != nil {
			w.logger.Warn("paj offramp completion: notification failed",
				zap.Error(err), zap.String("paj_order_id", c.PajOrderID))
		}
	}
	return nil
}

// reverseConfirmedFailure refunds the user when Circle confirms the USDC
// transfer failed. Shares the idempotency key with the webhook reversal path
// so a late webhook can't double-credit.
func (w *Worker) reverseConfirmedFailure(ctx context.Context, c stuckOrder) error {
	if c.HoldAmount.IsZero() || c.HoldAmount.IsNegative() {
		return nil
	}

	// Fail-fast BEFORE we mutate the order: claiming the row sets status=failed
	// and burns the deposit_id slot. If the ledger isn't wired, that claim would
	// leave the order permanently failed with no corresponding ledger refund —
	// the worst possible state for a money-moving path.
	if w.ledger == nil {
		w.logger.Error("CRITICAL: ledger reverser not configured for confirmed-failure reversal — skipping claim",
			zap.String("paj_order_id", c.PajOrderID),
			zap.String("user_id", c.UserID.String()))
		return fmt.Errorf("ledger reverser not configured")
	}

	var claimedHold decimal.Decimal
	err := w.db.QueryRowContext(ctx, `
		UPDATE paj_orders
		SET status = 'failed',
		    deposit_id = gen_random_uuid(),
		    last_webhook_status = 'auto-failed:circle-confirmed',
		    updated_at = NOW()
		WHERE paj_order_id = $1
		  AND status = 'pending'
		  AND deposit_id IS NULL
		RETURNING COALESCE(hold_amount, token_amount)`, c.PajOrderID).Scan(&claimedHold)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("claim order for reversal: %w", err)
	}
	if claimedHold.IsZero() || claimedHold.IsNegative() {
		return nil
	}

	if err := w.ledger.ReverseTransaction(ctx, c.UserID, entities.AccountTypeSpendingBalance,
		"paj-offramp-"+c.PajOrderID, claimedHold, map[string]interface{}{
			"provider":     "paj",
			"type":         "circle_confirmed_failure_reversal",
			"paj_order_id": c.PajOrderID,
			"fiat_amount":  c.FiatAmount,
		}); err != nil {
		return fmt.Errorf("reverse hold: %w", err)
	}

	w.logger.Info("Paj offramp auto-reversed (Circle FAILED)",
		zap.String("paj_order_id", c.PajOrderID),
		zap.String("user_id", c.UserID.String()),
		zap.String("amount", claimedHold.String()))
	return nil
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
		w.logger.Error("CRITICAL: ledger reverser not configured, order already claimed as failed",
			zap.String("paj_order_id", pajOrderID), zap.String("user_id", userID.String()))
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
		w.logger.Error("ledger reversal failed, order stuck in failed state",
			zap.String("paj_order_id", pajOrderID), zap.String("user_id", userID.String()), zap.Error(err))
		return fmt.Errorf("reverse hold: %w", err)
	}

	w.logger.Info("Paj offramp auto-reversed stuck order",
		zap.String("paj_order_id", pajOrderID),
		zap.String("user_id", userID.String()),
		zap.String("amount", claimedHold.String()),
		zap.Float64("fiat_amount", fiatAmount))
	return nil
}

// Package ramphub_onramp_recovery marks stuck RampHub onramp orders as failed so
// the user can retry. RampHub delivers USDC directly to the user's wallet, so
// successful deposits are credited by on-chain detection or the completed
// webhook; this worker only cleans up orders that never reached a terminal
// state. Mirrors the Paj onramp recovery worker.
package ramphub_onramp_recovery

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

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
		checkInterval: 5 * time.Minute,
		maxPendingAge: 1 * time.Hour,
		stopCh:        make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting RampHub onramp recovery worker",
		zap.Duration("interval", w.checkInterval),
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
	rows, err := w.db.QueryContext(ctx, `
		SELECT ramphub_transaction_id, user_id, fiat_amount, status, last_webhook_status
		FROM ramphub_orders
		WHERE order_type = 'onramp' AND status NOT IN ('completed','failed')
		  AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, int(w.maxPendingAge.Seconds()))
	if err != nil {
		w.logger.Error("ramphub onramp recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuck struct {
		TxID              string
		UserID            uuid.UUID
		FiatAmount        float64
		Status            string
		LastWebhookStatus *string
	}
	var orders []stuck
	for rows.Next() {
		var o stuck
		if err := rows.Scan(&o.TxID, &o.UserID, &o.FiatAmount, &o.Status, &o.LastWebhookStatus); err != nil {
			w.logger.Error("ramphub onramp recovery: scan failed", zap.Error(err))
			continue
		}
		orders = append(orders, o)
	}
	if len(orders) == 0 {
		return
	}

	for _, o := range orders {
		// If a completed webhook arrived but crediting hasn't been claimed, leave
		// it for the poll/webhook credit path or manual review rather than failing.
		if o.LastWebhookStatus != nil && strings.Contains(*o.LastWebhookStatus, "completed") {
			w.logger.Warn("ramphub onramp recovery: order saw completion but is uncredited — needs review",
				zap.String("ramphub_tx_id", o.TxID), zap.String("user_id", o.UserID.String()), zap.Float64("fiat_amount", o.FiatAmount))
			continue
		}
		if _, err := w.db.ExecContext(ctx, `
			UPDATE ramphub_orders SET status = 'failed', deposit_id = gen_random_uuid(), updated_at = NOW()
			WHERE ramphub_transaction_id = $1 AND status NOT IN ('completed','failed') AND deposit_id IS NULL`, o.TxID); err != nil {
			w.logger.Error("ramphub onramp recovery: mark failed", zap.Error(err), zap.String("ramphub_tx_id", o.TxID))
			continue
		}
		w.logger.Warn("ramphub onramp recovery: marked stuck order as failed — user should retry",
			zap.String("ramphub_tx_id", o.TxID), zap.String("user_id", o.UserID.String()), zap.Float64("fiat_amount", o.FiatAmount))
	}
}

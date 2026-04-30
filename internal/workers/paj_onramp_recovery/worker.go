package paj_onramp_recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PajClient is the subset of the PAJ client needed for polling order status.
type PajClient interface {
	GetTransaction(ctx context.Context, sessionToken, orderID string) (*PajTransaction, error)
}

// PajTransaction mirrors the fields we need from the PAJ API response.
type PajTransaction struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	USDCAmount float64 `json:"usdcAmount"`
	Amount     float64 `json:"amount"`
	Rate       float64 `json:"rate"`
}

// DepositCreditor credits a user's balance when an onramp order completes.
type DepositCreditor interface {
	CreditOnrampFromRecovery(ctx context.Context, userID uuid.UUID, pajOrderID string, usdcAmount float64, rate float64) error
}

// Worker periodically finds stuck PAJ onramp orders and attempts to resolve them.
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
	w.logger.Info("Starting PAJ onramp recovery worker",
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
	maxAgeSeconds := int(w.maxPendingAge.Seconds())

	// Find onramp orders stuck in pending/processing with no deposit_id (not yet credited)
	rows, err := w.db.QueryContext(ctx, `
		SELECT paj_order_id, user_id, fiat_amount, COALESCE(token_amount, 0), status
		FROM paj_orders
		WHERE order_type = 'onramp'
		  AND status NOT IN ('completed', 'failed')
		  AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1)
		LIMIT 10`, maxAgeSeconds)
	if err != nil {
		w.logger.Error("paj onramp recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	type stuckOrder struct {
		PajOrderID  string
		UserID      uuid.UUID
		FiatAmount  float64
		TokenAmount float64
		Status      string
	}

	var stuck []stuckOrder
	for rows.Next() {
		var o stuckOrder
		if err := rows.Scan(&o.PajOrderID, &o.UserID, &o.FiatAmount, &o.TokenAmount, &o.Status); err != nil {
			w.logger.Error("paj onramp recovery: scan failed", zap.Error(err))
			continue
		}
		stuck = append(stuck, o)
	}

	if len(stuck) == 0 {
		return
	}

	w.logger.Info("paj onramp recovery: found stuck orders", zap.Int("count", len(stuck)))

	for _, o := range stuck {
		// Mark as failed after being stuck too long — user can retry
		// We don't auto-credit because we can't verify payment without a PAJ session
		_, err := w.db.ExecContext(ctx, `
			UPDATE paj_orders
			SET status = 'failed', updated_at = NOW(),
				deposit_id = gen_random_uuid()
			WHERE paj_order_id = $1
			  AND status NOT IN ('completed', 'failed')
			  AND deposit_id IS NULL`,
			o.PajOrderID)
		if err != nil {
			w.logger.Error("paj onramp recovery: mark failed", zap.Error(err), zap.String("paj_order_id", o.PajOrderID))
			continue
		}

		w.logger.Warn("paj onramp recovery: marked stuck order as failed — user should retry or contact support",
			zap.String("paj_order_id", o.PajOrderID),
			zap.String("user_id", o.UserID.String()),
			zap.Float64("fiat_amount", o.FiatAmount))
	}
}

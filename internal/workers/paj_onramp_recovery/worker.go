package paj_onramp_recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// StuckOnrampResolver re-verifies a stuck onramp order against Paj's API and
// applies the verified outcome (status persist + credit on completion).
// Returns the verified local status ("pending", "paid", "completed",
// "failed"). A non-nil error means the order could not be verified and must
// be left alone. Implemented by *pajfunding.Service; wired via SetResolver.
type StuckOnrampResolver interface {
	RecoverStuckOnramp(ctx context.Context, userID uuid.UUID, pajOrderID string) (string, error)
}

// abandonedAfterAge bounds how long an order Paj still reports as INIT (user
// never paid) is kept before being failed so the user can retry. Orders Paj
// reports as PAID or better are never fail-marked — funds may be with Paj.
const abandonedAfterAge = 24 * time.Hour

// Worker periodically finds stuck PAJ onramp orders and resolves them against
// Paj's API via the resolver — never by guessing.
type Worker struct {
	db            *sql.DB
	resolver      StuckOnrampResolver
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

// SetResolver wires live Paj verification + crediting. Without it the worker
// cannot verify provider truth, so it leaves stuck orders alone instead of
// fail-marking orders whose funds may already be with Paj.
func (w *Worker) SetResolver(r StuckOnrampResolver) { w.resolver = r }

type stuckOrder struct {
	PajOrderID string
	UserID     uuid.UUID
	FiatAmount float64
	CreatedAt  time.Time
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
	if w.resolver == nil {
		w.logger.Warn("paj onramp recovery: no resolver wired — leaving stuck orders for webhook/user poll (refusing to fail-mark unverified orders)")
		return
	}
	maxAgeSeconds := int(w.maxPendingAge.Seconds())

	// Find onramp orders stuck in pending/processing with no deposit_id (not yet credited).
	rows, err := w.db.QueryContext(ctx, `
	SELECT paj_order_id, user_id, fiat_amount, created_at
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

	var stuck []stuckOrder
	for rows.Next() {
		var o stuckOrder
		if err := rows.Scan(&o.PajOrderID, &o.UserID, &o.FiatAmount, &o.CreatedAt); err != nil {
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
		// Re-verify against Paj's API — the resolver persists the verified
		// status and credits on completion.
		status, err := w.resolver.RecoverStuckOnramp(ctx, o.UserID, o.PajOrderID)
		if err != nil {
			// Unverifiable (no session, provider unreachable): leave the
			// order for the user's re-authenticated poll or a later tick.
			// Never fail-mark what we cannot verify.
			w.logger.Info("paj onramp recovery: order unverifiable, leaving for user poll/retry",
				zap.String("paj_order_id", o.PajOrderID),
				zap.String("user_id", o.UserID.String()),
				zap.Error(err))
			continue
		}
		switch status {
		case "completed", "failed":
			w.logger.Info("paj onramp recovery: order resolved from provider truth",
				zap.String("paj_order_id", o.PajOrderID),
				zap.String("status", status))
		case "pending":
			// Paj reports INIT: the user never paid. Only fail-mark once the
			// order is old enough to be abandoned — anything fresher (or
			// PAID, handled below) stays until the webhook lands.
			if time.Since(o.CreatedAt) < abandonedAfterAge {
				continue
			}
			w.failAbandoned(ctx, o)
		default:
			// "paid" or anything in flight — leave for the webhook.
		}
	}
}

// failAbandoned marks a never-paid, day-old order failed so the user can
// retry. Only called for orders Paj itself reports as INIT.
func (w *Worker) failAbandoned(ctx context.Context, o stuckOrder) {
	_, err := w.db.ExecContext(ctx, `
	UPDATE paj_orders
	SET status = 'failed', updated_at = NOW(),
		deposit_id = gen_random_uuid()
	WHERE paj_order_id = $1
	  AND status NOT IN ('completed', 'failed')
	  AND deposit_id IS NULL`,
		o.PajOrderID)
	if err != nil {
		w.logger.Error("paj onramp recovery: mark abandoned failed", zap.Error(err), zap.String("paj_order_id", o.PajOrderID))
		return
	}
	w.logger.Warn("paj onramp recovery: marked never-paid order as failed after 24h — user can retry",
		zap.String("paj_order_id", o.PajOrderID),
		zap.String("user_id", o.UserID.String()),
		zap.Float64("fiat_amount", o.FiatAmount))
}

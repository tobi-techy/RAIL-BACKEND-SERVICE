package graph_ngn_recovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"go.uber.org/zap"
)

// NGNDepositResumer re-drives a stuck Graph NGN deposit. Implemented by
// funding.GraphVirtualAccountService; ProcessNGNDeposit is idempotent (the
// conversion is deduped by Graph reference and the ledger credit by deposit ID),
// so resuming a pending row cannot double-spend.
type NGNDepositResumer interface {
	ProcessNGNDeposit(ctx context.Context, event *funding.GraphNGNDepositEvent) error
}

// Worker periodically re-drives Graph NGN deposits stuck in a non-terminal state
// (a missed/failed webhook, a transient conversion/rate outage, or a crash
// between the idempotency claim and the ledger commit). Deposits between
// maxPendingAge and maxRetryAge are actively resumed; anything older is only
// surfaced for manual review to avoid retrying a permanently failing deposit
// forever.
type Worker struct {
	db            *sql.DB
	resumer       NGNDepositResumer
	logger        *zap.Logger
	checkInterval time.Duration
	maxPendingAge time.Duration
	maxRetryAge   time.Duration
	stopCh        chan struct{}
}

func NewWorker(db *sql.DB, resumer NGNDepositResumer, logger *zap.Logger) *Worker {
	return &Worker{
		db:            db,
		resumer:       resumer,
		logger:        logger,
		checkInterval: 10 * time.Minute,
		maxPendingAge: 15 * time.Minute,
		maxRetryAge:   24 * time.Hour,
		stopCh:        make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting Graph NGN deposit recovery worker",
		zap.Duration("interval", w.checkInterval),
		zap.Duration("max_pending_age", w.maxPendingAge),
		zap.Duration("max_retry_age", w.maxRetryAge),
		zap.Bool("auto_resume", w.resumer != nil))

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

type stuckDeposit struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Amount         float64
	TxHash         string
	GraphAccountID string
	SourceAmount   sql.NullString
	SourceCurrency sql.NullString
	CreatedAt      time.Time
}

func (w *Worker) recover(ctx context.Context) {
	minAgeSeconds := int(w.maxPendingAge.Seconds())

	rows, err := w.db.QueryContext(ctx, `
		SELECT d.id, d.user_id, d.amount, d.tx_hash,
		       COALESCE(va.graph_account_id, ''),
		       d.source_amount::text, d.source_currency, d.created_at
		FROM deposits d
		JOIN virtual_accounts va ON d.virtual_account_id = va.id
		WHERE va.provider = 'graph'
		  AND d.chain = 'fiat'
		  AND d.status IN ('pending', 'processing')
		  AND d.created_at < NOW() - make_interval(secs => $1)
		ORDER BY d.created_at ASC
		LIMIT 50`, minAgeSeconds)
	if err != nil {
		w.logger.Error("graph ngn recovery: query failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var stuck []stuckDeposit
	for rows.Next() {
		var d stuckDeposit
		if err := rows.Scan(&d.ID, &d.UserID, &d.Amount, &d.TxHash, &d.GraphAccountID, &d.SourceAmount, &d.SourceCurrency, &d.CreatedAt); err != nil {
			w.logger.Error("graph ngn recovery: scan failed", zap.Error(err))
			continue
		}
		stuck = append(stuck, d)
	}

	if len(stuck) == 0 {
		return
	}

	now := time.Now()
	for _, d := range stuck {
		tooOld := now.Sub(d.CreatedAt) > w.maxRetryAge
		canResume := w.resumer != nil && !tooOld && d.GraphAccountID != "" && d.SourceAmount.Valid && d.SourceAmount.String != ""

		if !canResume {
			w.logger.Warn("graph ngn recovery: stuck NGN deposit needs manual review",
				zap.String("deposit_id", d.ID.String()),
				zap.String("user_id", d.UserID.String()),
				zap.String("tx_hash", d.TxHash),
				zap.Float64("usdc_amount", d.Amount),
				zap.Bool("too_old", tooOld),
				zap.Time("created_at", d.CreatedAt))
			continue
		}

		event := &funding.GraphNGNDepositEvent{
			GraphAccountID: d.GraphAccountID,
			TransactionID:  d.TxHash,
			AmountNGN:      d.SourceAmount.String,
			Direction:      "credit",
		}
		if err := w.resumer.ProcessNGNDeposit(ctx, event); err != nil {
			w.logger.Warn("graph ngn recovery: resume attempt failed (will retry next cycle)",
				zap.String("deposit_id", d.ID.String()),
				zap.String("tx_hash", d.TxHash),
				zap.Error(err))
			continue
		}
		w.logger.Info("graph ngn recovery: resumed stuck NGN deposit",
			zap.String("deposit_id", d.ID.String()),
			zap.String("tx_hash", d.TxHash))
	}
}

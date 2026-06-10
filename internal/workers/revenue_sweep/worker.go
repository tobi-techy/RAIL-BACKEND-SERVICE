package revenue_sweep

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// TransferAdapter performs revenue transfers to the treasury wallet.
type TransferAdapter interface {
	TransferToTreasury(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error
}

// Worker periodically sweeps accumulated fee revenue to the treasury wallet.
type Worker struct {
	transfer       TransferAdapter
	db             *sqlx.DB
	minAmount      decimal.Decimal
	interval       time.Duration
	logger         *zap.Logger
	stopCh         chan struct{}
	stopOnce       sync.Once
}

// NewWorker creates a revenue sweep worker.
func NewWorker(transfer TransferAdapter, db *sqlx.DB, minAmount decimal.Decimal, interval time.Duration, logger *zap.Logger) *Worker {
	return &Worker{
		transfer:  transfer,
		db:        db,
		minAmount: minAmount,
		interval:  interval,
		logger:    logger,
		stopCh:    make(chan struct{}),
	}
}

// Start begins the periodic sweep loop.
func (w *Worker) Start() {
	go w.loop()
}

// Stop gracefully stops the worker.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

func (w *Worker) loop() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if err := w.sweep(); err != nil {
				w.logger.Error("revenue sweep failed", zap.Error(err))
			}
		}
	}
}

func (w *Worker) sweep() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Query revenue accounts with balance above minimum
	type revenueRow struct {
		UserID  uuid.UUID       `db:"user_id"`
		Balance decimal.Decimal `db:"balance"`
	}

	var rows []revenueRow
	err := w.db.SelectContext(ctx, &rows,
		`SELECT user_id, balance FROM ledger_accounts
		 WHERE account_type IN ('withdrawal_fee_revenue', 'emergency_withdrawal_revenue', 'subscription_revenue')
		   AND balance >= $1`,
		w.minAmount)
	if err != nil {
		return err
	}

	for _, row := range rows {
		ref := "revenue_sweep:" + uuid.New().String()
		if err := w.transfer.TransferToTreasury(ctx, row.UserID, row.Balance, ref); err != nil {
			w.logger.Warn("revenue sweep transfer failed",
				zap.String("user_id", row.UserID.String()),
				zap.String("amount", row.Balance.String()),
				zap.Error(err))
			continue
		}
		w.logger.Info("revenue swept",
			zap.String("user_id", row.UserID.String()),
			zap.String("amount", row.Balance.String()))
	}
	return nil
}

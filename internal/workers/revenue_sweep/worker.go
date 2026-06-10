package revenue_sweep

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// TransferService moves USDC from a user's custody wallet to the treasury.
type TransferService interface {
	TransferToTreasury(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error
}

// Worker periodically sweeps fee revenue to the company treasury wallet.
//
// Revenue from withdrawal fees and emergency withdrawal fees stays in users'
// Circle wallets (only the ledger balance is decremented). This worker finds
// users with unswept fee revenue and transfers USDC from their wallets to the
// treasury, then zeroes the ledger revenue account.
type Worker struct {
	transfer       TransferService
	db             *sqlx.DB
	minSweepAmount decimal.Decimal
	interval       time.Duration
	logger         *zap.Logger
	stopCh         chan struct{}
	stopOnce       sync.Once
	sweeping       int32
}

func NewWorker(
	transfer TransferService,
	db *sqlx.DB,
	minSweepAmount decimal.Decimal,
	interval time.Duration,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		transfer:       transfer,
		db:             db,
		minSweepAmount: minSweepAmount,
		interval:       interval,
		logger:         logger,
		stopCh:         make(chan struct{}),
	}
}

func (w *Worker) Start() {
	ticker := time.NewTicker(w.interval)
	go func() {
		w.run()
		for {
			select {
			case <-ticker.C:
				w.run()
			case <-w.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

func (w *Worker) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := w.sweep(ctx); err != nil {
		w.logger.Error("Revenue sweep failed", zap.Error(err))
	}
}

type unsweptFee struct {
	ID     uuid.UUID       `db:"id"`
	UserID uuid.UUID       `db:"user_id"`
	Amount decimal.Decimal `db:"fee_amount"`
}

func (w *Worker) sweep(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&w.sweeping, 0, 1) {
		return nil
	}
	defer atomic.StoreInt32(&w.sweeping, 0)

	if w.db == nil {
		return nil
	}

	// Find completed withdrawals with fees that haven't been swept to treasury yet.
	var fees []unsweptFee
	err := w.db.SelectContext(ctx, &fees, `
		SELECT id, user_id, fee_amount FROM withdrawals
		WHERE status = 'completed' AND fee_amount > 0 AND fee_swept = FALSE
		ORDER BY created_at ASC LIMIT 50`)
	if err != nil {
		return fmt.Errorf("query unswept fees: %w", err)
	}

	if len(fees) == 0 {
		w.logger.Info("Revenue sweep: no unswept fees found")
		return nil
	}

	w.logger.Info("Revenue sweep: found unswept fees", zap.Int("count", len(fees)))

	var swept, failed int
	for _, f := range fees {
		if f.Amount.LessThan(w.minSweepAmount) {
			continue
		}

		ref := fmt.Sprintf("fee-sweep-%s", f.ID.String())
		if err := w.transfer.TransferToTreasury(ctx, f.UserID, f.Amount, ref); err != nil {
			w.logger.Warn("Revenue sweep transfer failed",
				zap.String("withdrawal_id", f.ID.String()),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			failed++
			continue
		}

		// Mark as swept
		if _, err := w.db.ExecContext(ctx, `UPDATE withdrawals SET fee_swept = TRUE WHERE id = $1`, f.ID); err != nil {
			// CRITICAL: Fee transfer succeeded but DB mark failed. Money moved but fee
			// will be selected again since fee_swept is still FALSE. Count as swept for
			// metrics accuracy. Requires manual reconciliation.
			w.logger.Error("CRITICAL: fee transfer succeeded but failed to mark as swept — requires manual reconciliation",
				zap.String("withdrawal_id", f.ID.String()),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			swept++
			continue
		}
		swept++
	}

	if swept > 0 || failed > 0 {
		w.logger.Info("Revenue sweep cycle complete",
			zap.Int("swept", swept), zap.Int("failed", failed))
	}
	return nil
}

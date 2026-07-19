package revenue_sweep

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		// #region agent log
		writeAgentDebugLog("revenue_sweep/worker.go:sweep", "revenue sweep skipped: db nil", "H1", map[string]interface{}{"enabled": false})
		// #endregion
		return nil
	}

	// #region agent log
	writeAgentDebugLog("revenue_sweep/worker.go:sweep", "revenue sweep cycle started", "H2", map[string]interface{}{
		"interval_hours": w.interval.Hours(), "min_sweep_amount": w.minSweepAmount.String(),
	})
	// #endregion

	wSwept, wFailed, wErr := w.sweepWithdrawalFees(ctx)
	pSwept, pFailed, pErr := w.sweepPajOfframpFees(ctx)

	total := wSwept + pSwept
	totalFailed := wFailed + pFailed
	if total > 0 || totalFailed > 0 {
		w.logger.Info("Revenue sweep cycle complete",
			zap.Int("withdrawal_swept", wSwept),
			zap.Int("withdrawal_failed", wFailed),
			zap.Int("paj_offramp_swept", pSwept),
			zap.Int("paj_offramp_failed", pFailed))
	}
	if wErr != nil {
		return wErr
	}
	return pErr
}

func (w *Worker) sweepWithdrawalFees(ctx context.Context) (int, int, error) {
	var fees []unsweptFee
	err := w.db.SelectContext(ctx, &fees, `
		SELECT id, user_id, fee_amount FROM withdrawals
		WHERE status = 'completed' AND fee_amount > 0 AND fee_swept = FALSE
		ORDER BY created_at ASC LIMIT 50`)
	if err != nil {
		return 0, 0, fmt.Errorf("query unswept withdrawal fees: %w", err)
	}

	if len(fees) == 0 {
		return 0, 0, nil
	}

	// #region agent log
	writeAgentDebugLog("revenue_sweep/worker.go:sweepWithdrawalFees", "unswept withdrawal fees found", "H5", map[string]interface{}{
		"count": len(fees),
	})
	// #endregion

	w.logger.Info("Revenue sweep: found unswept withdrawal fees", zap.Int("count", len(fees)))

	var swept, failed int
	for _, f := range fees {
		if f.Amount.LessThan(w.minSweepAmount) {
			continue
		}

		// Circle requires idempotencyKey to be a valid UUID. Derive a deterministic
		// UUID from the withdrawal ID so retries are idempotent without violating format.
		ref := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("fee-sweep-%s", f.ID.String()))).String()
		if err := w.transfer.TransferToTreasury(ctx, f.UserID, f.Amount, ref); err != nil {
			// #region agent log
			writeAgentDebugLog("revenue_sweep/worker.go:sweepWithdrawalFees", "treasury transfer failed", "H4", map[string]interface{}{
				"withdrawal_id": f.ID.String(), "user_id": f.UserID.String(), "amount": f.Amount.String(), "error": err.Error(),
			})
			// #endregion
			w.logger.Warn("Revenue sweep transfer failed",
				zap.String("withdrawal_id", f.ID.String()),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			failed++
			continue
		}

		// #region agent log
		writeAgentDebugLog("revenue_sweep/worker.go:sweepWithdrawalFees", "treasury transfer succeeded", "H4", map[string]interface{}{
			"withdrawal_id": f.ID.String(), "user_id": f.UserID.String(), "amount": f.Amount.String(),
		})
		// #endregion

		if _, err := w.db.ExecContext(ctx, `UPDATE withdrawals SET fee_swept = TRUE WHERE id = $1`, f.ID); err != nil {
			// CRITICAL: Fee transfer succeeded but DB mark failed. Money moved but fee
			// will be selected again since fee_swept is still FALSE. Requires manual
			// reconciliation; count as swept so the metric reflects the on-chain reality.
			w.logger.Error("CRITICAL: withdrawal fee transfer succeeded but failed to mark as swept — requires manual reconciliation",
				zap.String("withdrawal_id", f.ID.String()),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			swept++
			continue
		}
		swept++
	}
	return swept, failed, nil
}

type unsweptPajFee struct {
	PajOrderID string          `db:"paj_order_id"`
	UserID     uuid.UUID       `db:"user_id"`
	Amount     decimal.Decimal `db:"rail_fee_usdc"`
}

// sweepPajOfframpFees moves the per-offramp Rail fee out of the user's Circle
// wallet to the treasury. The fee was already booked to the
// withdrawal_fee_revenue ledger account at hold time; this step makes the USDC
// physically follow the ledger entry.
func (w *Worker) sweepPajOfframpFees(ctx context.Context) (int, int, error) {
	var fees []unsweptPajFee
	err := w.db.SelectContext(ctx, &fees, `
		SELECT paj_order_id, user_id, rail_fee_usdc FROM paj_orders
		WHERE order_type = 'offramp'
		  AND status = 'completed'
		  AND rail_fee_usdc IS NOT NULL
		  AND rail_fee_usdc > 0
		  AND fee_swept = FALSE
		ORDER BY created_at ASC LIMIT 50`)
	if err != nil {
		return 0, 0, fmt.Errorf("query unswept paj offramp fees: %w", err)
	}

	if len(fees) == 0 {
		return 0, 0, nil
	}

	w.logger.Info("Revenue sweep: found unswept paj offramp fees", zap.Int("count", len(fees)))

	var swept, failed int
	for _, f := range fees {
		if f.Amount.LessThan(w.minSweepAmount) {
			continue
		}

		ref := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("paj-offramp-fee-sweep-%s", f.PajOrderID))).String()
		if err := w.transfer.TransferToTreasury(ctx, f.UserID, f.Amount, ref); err != nil {
			w.logger.Warn("Paj offramp revenue sweep transfer failed",
				zap.String("paj_order_id", f.PajOrderID),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			failed++
			continue
		}

		if _, err := w.db.ExecContext(ctx,
			`UPDATE paj_orders SET fee_swept = TRUE, fee_swept_at = NOW() WHERE paj_order_id = $1`,
			f.PajOrderID); err != nil {
			// CRITICAL: Fee transfer succeeded but DB mark failed. Money moved but fee
			// will be selected again since fee_swept is still FALSE. This requires manual
			// reconciliation to prevent double-counting in metrics.
			w.logger.Error("CRITICAL: paj offramp fee transfer succeeded but failed to mark as swept — requires manual reconciliation",
				zap.String("paj_order_id", f.PajOrderID),
				zap.String("user_id", f.UserID.String()),
				zap.Error(err))
			// Count as swept because the on-chain transfer already happened.
			swept++
			continue
		}
		swept++
	}
	return swept, failed, nil
}

// #region agent log
func writeAgentDebugLog(location, message, hypothesisID string, data map[string]interface{}) {
	payload := map[string]interface{}{
		"sessionId": "b38437", "location": location, "message": message,
		"hypothesisId": hypothesisID, "data": data, "timestamp": time.Now().UnixMilli(),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	f, err := os.OpenFile("/Users/tobi/Development/RAIL_BACKEND/.cursor/debug-b38437.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// #endregion

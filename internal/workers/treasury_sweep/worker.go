package treasury_sweep

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/reflect"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// LedgerReader returns the aggregate stash balance across all users.
type LedgerReader interface {
	GetTotalStashBalance(ctx context.Context) (decimal.Decimal, error)
}

// DistributedYieldReader returns the total yield ever distributed to users.
// The sweep compares ledger principal (stash - distributed) against the Reflect position.
type DistributedYieldReader interface {
	GetTotalDistributedYield(ctx context.Context) (decimal.Decimal, error)
}

// CircleTransferSource is the subset of Circle wallet operations needed to fund
// Rail's Solana Reflect wallet from Circle custody.
type CircleTransferSource interface {
	GetUSDCTokenID(ctx context.Context, walletID string) (string, error)
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circle.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circle.Transaction, error)
}

// Worker periodically sweeps aggregate stash USDC into the Reflect yield pool.
//
// Two-phase flow:
//  1. Transfer USDC from Circle custody wallet → Rail's Solana wallet (via Circle API)
//  2. Mint USDC+ from Solana wallet → Reflect (via Reflect tx on Solana)
type Worker struct {
	reflect            *reflect.Client
	circle             CircleTransferSource
	ledger             LedgerReader
	distributedYield   DistributedYieldReader
	db                 *sqlx.DB
	circleSourceWallet string
	solanaWallet       string
	minSweepAmount     decimal.Decimal
	interval           time.Duration
	logger             *zap.Logger
	stopCh             chan struct{}
	stopOnce           sync.Once
	sweeping           int32
}

// NewWorker creates a treasury sweep worker.
func NewWorker(
	reflectClient *reflect.Client,
	circleSource CircleTransferSource,
	ledger LedgerReader,
	distributedYield DistributedYieldReader,
	db *sqlx.DB,
	circleSourceWallet string,
	solanaWallet string,
	minSweepAmount decimal.Decimal,
	interval time.Duration,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		reflect:            reflectClient,
		circle:             circleSource,
		ledger:             ledger,
		distributedYield:   distributedYield,
		db:                 db,
		circleSourceWallet: circleSourceWallet,
		solanaWallet:       solanaWallet,
		minSweepAmount:     minSweepAmount,
		interval:           interval,
		logger:             logger,
		stopCh:             make(chan struct{}),
	}
}

// Start begins the periodic sweep loop.
func (w *Worker) Start() {
	ticker := time.NewTicker(w.interval)
	go func() {
		w.runSweep()
		for {
			select {
			case <-ticker.C:
				w.runSweep()
			case <-w.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

func (w *Worker) runSweep() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := w.sweep(ctx); err != nil {
		w.logger.Error("Treasury sweep failed", zap.Error(err))
	}
}

// Stop signals the worker to shut down. Safe to call multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

func (w *Worker) sweep(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&w.sweeping, 0, 1) {
		w.logger.Debug("Skipping sweep: previous sweep still running")
		return nil
	}
	defer atomic.StoreInt32(&w.sweeping, 0)

	ledgerTotal, err := w.ledger.GetTotalStashBalance(ctx)
	if err != nil {
		return fmt.Errorf("get ledger stash total: %w", err)
	}

	totalDistributed, err := w.distributedYield.GetTotalDistributedYield(ctx)
	if err != nil {
		return fmt.Errorf("get total distributed yield: %w", err)
	}
	// Ledger stash includes yield credited to users; Reflect holds only principal.
	// Compare principal-only to avoid perpetual deposit loop after yield distribution.
	ledgerPrincipal := ledgerTotal.Sub(totalDistributed)

	var depositedUSDC decimal.Decimal
	if err := w.db.GetContext(ctx, &depositedUSDC,
		`SELECT value FROM yield_state WHERE key = 'reflect_deposited_usdc'`); err != nil {
		return fmt.Errorf("get reflect deposited usdc: %w", err)
	}

	pendingRoutes, err := w.pendingReflectRoutesAmount(ctx)
	if err != nil {
		return fmt.Errorf("get pending reflect routes amount: %w", err)
	}

	// Compare ledger principal against raw deposited USDC (not rate-multiplied).
	// For deposits, exclude funds already claimed by the per-user Circle Reflect
	// router so treasury backfill does not duplicate an in-flight user route.
	// Invariant: ledgerPrincipal == depositedUSDC + pendingRoutes + unsweptBacklog
	// Rate appreciation is unrealised yield — not a sweep trigger.
	// We only sweep when users deposit/withdraw, changing the principal.
	diff := ledgerPrincipal.Sub(depositedUSDC)
	if diff.IsPositive() {
		diff = diff.Sub(pendingRoutes)
		if diff.IsNegative() {
			diff = decimal.Zero
		}
	}

	// Still need rate for the withdraw path (to compute token amount to burn).
	var rate decimal.Decimal
	if diff.IsNegative() && diff.Abs().GreaterThanOrEqual(w.minSweepAmount) {
		rate, err = w.reflect.GetExchangeRate(ctx)
		if err != nil {
			return fmt.Errorf("get reflect exchange rate: %w", err)
		}
	}

	w.logger.Info("Treasury sweep check",
		zap.String("ledger_total", ledgerTotal.StringFixed(6)),
		zap.String("total_distributed", totalDistributed.StringFixed(6)),
		zap.String("ledger_principal", ledgerPrincipal.StringFixed(6)),
		zap.String("reflect_deposited_usdc", depositedUSDC.StringFixed(6)),
		zap.String("pending_reflect_routes", pendingRoutes.StringFixed(6)),
		zap.String("diff", diff.StringFixed(6)),
		zap.String("min_sweep_amount", w.minSweepAmount.StringFixed(6)),
	)

	if diff.Abs().LessThan(w.minSweepAmount) {
		w.logger.Info("Treasury sweep skipped: diff below minimum",
			zap.String("diff", diff.StringFixed(6)),
			zap.String("min_sweep_amount", w.minSweepAmount.StringFixed(6)))
		return nil
	}

	if diff.IsPositive() {
		sweepKey := deterministicSweepKey("deposit", depositedUSDC, diff)
		w.logger.Info("Treasury sweep depositing principal into Reflect",
			zap.String("amount", diff.StringFixed(6)),
			zap.String("circle_source_wallet_id", w.circleSourceWallet),
			zap.String("solana_wallet", w.solanaWallet),
			zap.String("sweep_key", sweepKey))
		return w.deposit(ctx, diff, sweepKey)
	}
	sweepKey := deterministicSweepKey("withdrawal", depositedUSDC, diff.Abs())
	w.logger.Info("Treasury sweep withdrawing principal from Reflect",
		zap.String("amount", diff.Abs().StringFixed(6)),
		zap.String("exchange_rate", rate.String()),
		zap.String("sweep_key", sweepKey))
	return w.withdraw(ctx, diff.Abs(), rate)
}

// deposit executes the two-phase flow: Circle→Solana, then Solana→Reflect mint.
func (w *Worker) deposit(ctx context.Context, amount decimal.Decimal, sweepKey string) error {
	circleTxID, err := w.fundSolanaWallet(ctx, amount, sweepKey)
	if err != nil {
		return fmt.Errorf("fund solana wallet: %w", err)
	}
	if err := w.waitForCircleTransfer(ctx, circleTxID); err != nil {
		return fmt.Errorf("circle transfer did not settle: %w", err)
	}

	txHash, err := w.reflect.Mint(ctx, amount)
	if err != nil {
		return fmt.Errorf("reflect mint: %w", err)
	}

	// Atomically record the operation and update deposited USDC in one transaction.
	return w.recordOperationAndUpdateDeposit(ctx, "deposit", amount, txHash)
}

func (w *Worker) withdraw(ctx context.Context, usdcAmount decimal.Decimal, rate decimal.Decimal) error {
	if rate.IsZero() {
		return fmt.Errorf("exchange rate is zero, cannot compute burn amount")
	}
	// Convert USDC amount to USDC+ tokens: tokens = usdc / rate
	tokenAmount := usdcAmount.Div(rate).Truncate(6)

	txHash, err := w.reflect.Burn(ctx, tokenAmount)
	if err != nil {
		return fmt.Errorf("reflect burn: %w", err)
	}

	// Record the USDC equivalent withdrawn so reflect_deposited_usdc stays in USDC terms.
	return w.recordOperationAndUpdateDeposit(ctx, "withdrawal", usdcAmount, txHash)
}

// fundSolanaWallet creates a Circle transfer from the custody wallet to the Solana wallet.
func (w *Worker) fundSolanaWallet(ctx context.Context, amount decimal.Decimal, sweepKey string) (string, error) {
	tokenID, err := w.circle.GetUSDCTokenID(ctx, w.circleSourceWallet)
	if err != nil {
		return "", fmt.Errorf("get circle usdc token id: %w", err)
	}

	transfer, err := w.circle.TransferUSDCWithIdempotency(
		ctx,
		w.circleSourceWallet,
		tokenID,
		w.solanaWallet,
		amount.Truncate(6).StringFixed(6),
		sweepKey,
	)
	if err != nil {
		return "", fmt.Errorf("create circle transfer: %w", err)
	}

	w.logger.Info("Circle→Solana transfer created",
		zap.String("transfer_id", transfer.ID),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("circle_source_wallet_id", w.circleSourceWallet),
		zap.String("solana_wallet", w.solanaWallet),
	)
	return transfer.ID, nil
}

func deterministicSweepKey(operation string, depositedUSDC, amount decimal.Decimal) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf(
		"reflect-sweep:%s:%s:%s",
		operation,
		depositedUSDC.StringFixed(6),
		amount.StringFixed(6),
	))).String()
}

// waitForCircleTransfer polls the Circle transfer until it settles or fails.
func (w *Worker) waitForCircleTransfer(ctx context.Context, transferID string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for transfer %s", transferID)
		case <-ticker.C:
			transfer, err := w.circle.GetTransaction(ctx, transferID)
			if err != nil {
				w.logger.Warn("Failed to poll circle transfer", zap.String("id", transferID), zap.Error(err))
				continue
			}

			switch transfer.State {
			case circle.TransactionStateComplete:
				w.logger.Info("Circle transfer settled", zap.String("id", transferID), zap.String("tx_hash", transfer.TxHash))
				return nil
			case circle.TransactionStateInitiated, circle.TransactionStateQueued, circle.TransactionStateSent:
				continue
			case circle.TransactionStateFailed, circle.TransactionStateCancelled, circle.TransactionStateDenied:
				return fmt.Errorf("circle transfer %s failed with state %s: %s", transferID, transfer.State, transfer.ErrorReason)
			default:
				w.logger.Warn("Circle transfer returned unknown state; continuing poll",
					zap.String("id", transferID),
					zap.String("state", string(transfer.State)))
				continue
			}
		}
	}
}

func (w *Worker) pendingReflectRoutesAmount(ctx context.Context) (decimal.Decimal, error) {
	if w.db == nil {
		return decimal.Zero, nil
	}

	var pending decimal.Decimal
	if err := w.db.GetContext(ctx, &pending, `
		SELECT COALESCE(SUM(amount), 0)
		FROM reflect_deposit_routes
		WHERE status <> 'complete'
	`); err != nil {
		if isUndefinedTableError(err) {
			return decimal.Zero, nil
		}
		return decimal.Zero, err
	}
	return pending, nil
}

func isUndefinedTableError(err error) bool {
	var pqErr *pq.Error
	return err != nil && errors.As(err, &pqErr) && pqErr.Code == "42P01"
}

// recordOperationAndUpdateDeposit atomically records the treasury operation and
// updates the reflect_deposited_usdc balance in a single DB transaction.
// This prevents the tracked balance from drifting if the process crashes between
// the Solana tx confirming and the DB write.
func (w *Worker) recordOperationAndUpdateDeposit(ctx context.Context, opType string, amount decimal.Decimal, txHash string) error {
	tx, err := w.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO treasury_positions (id, operation, amount, tx_hash) VALUES ($1, $2, $3, $4)`,
		uuid.New(), opType, amount, txHash,
	); err != nil {
		return fmt.Errorf("insert treasury position: %w", err)
	}

	var updateSQL string
	if opType == "deposit" {
		updateSQL = `UPDATE yield_state SET value = value::numeric + $1, updated_at = NOW() WHERE key = 'reflect_deposited_usdc'`
	} else {
		updateSQL = `UPDATE yield_state SET value = GREATEST(0, value::numeric - $1), updated_at = NOW() WHERE key = 'reflect_deposited_usdc'`
	}
	if _, err := tx.ExecContext(ctx, updateSQL, amount); err != nil {
		return fmt.Errorf("update reflect_deposited_usdc: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	w.logger.Info("Treasury sweep completed",
		zap.String("operation", opType),
		zap.String("amount", amount.StringFixed(6)),
		zap.String("reflect_tx", txHash),
	)
	return nil
}

package treasury_sweep

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
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

// Worker periodically sweeps aggregate stash USDC into the Reflect yield pool.
//
// Two-phase flow:
//  1. Transfer USDC from Bridge custody wallet → Rail's Solana wallet (via Bridge API)
//  2. Mint USDC+ from Solana wallet → Reflect (via Reflect tx on Solana)
type Worker struct {
	reflect            *reflect.Client
	bridgeClient       *bridge.Client
	ledger             LedgerReader
	distributedYield   DistributedYieldReader
	db                 *sqlx.DB
	bridgeCustomerID   string
	bridgeSourceWallet string
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
	bridgeClient *bridge.Client,
	ledger LedgerReader,
	distributedYield DistributedYieldReader,
	db *sqlx.DB,
	bridgeCustomerID string,
	bridgeSourceWallet string,
	solanaWallet string,
	minSweepAmount decimal.Decimal,
	interval time.Duration,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		reflect:            reflectClient,
		bridgeClient:       bridgeClient,
		ledger:             ledger,
		distributedYield:   distributedYield,
		db:                 db,
		bridgeCustomerID:   bridgeCustomerID,
		bridgeSourceWallet: bridgeSourceWallet,
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

	// Compare ledger principal against raw deposited USDC (not rate-multiplied).
	// Invariant: ledgerPrincipal == depositedUSDC
	// Rate appreciation is unrealised yield — not a sweep trigger.
	// We only sweep when users deposit/withdraw, changing the principal.
	diff := ledgerPrincipal.Sub(depositedUSDC)

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
		zap.String("diff", diff.StringFixed(6)),
	)

	if diff.Abs().LessThan(w.minSweepAmount) {
		return nil
	}

	if diff.IsPositive() {
		return w.deposit(ctx, diff)
	}
	return w.withdraw(ctx, diff.Abs(), rate)
}

// deposit executes the two-phase flow: Bridge→Solana, then Solana→Reflect mint.
func (w *Worker) deposit(ctx context.Context, amount decimal.Decimal) error {
	bridgeTxID, err := w.fundSolanaWallet(ctx, amount)
	if err != nil {
		return fmt.Errorf("fund solana wallet: %w", err)
	}
	if err := w.waitForBridgeTransfer(ctx, bridgeTxID); err != nil {
		return fmt.Errorf("bridge transfer did not settle: %w", err)
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

// fundSolanaWallet creates a Bridge transfer from the custody wallet to the Solana wallet.
func (w *Worker) fundSolanaWallet(ctx context.Context, amount decimal.Decimal) (string, error) {
	req := &bridge.CreateTransferRequest{
		ClientReferenceID: fmt.Sprintf("reflect-sweep-%s", uuid.New().String()[:8]),
		OnBehalfOf:        w.bridgeCustomerID,
		Amount:            amount.Truncate(6).String(),
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRailSolana,
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: w.bridgeSourceWallet,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: bridge.PaymentRailSolana,
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   w.solanaWallet,
		},
	}

	transfer, err := w.bridgeClient.CreateTransfer(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create bridge transfer: %w", err)
	}

	w.logger.Info("Bridge→Solana transfer created",
		zap.String("transfer_id", transfer.ID),
		zap.String("amount", amount.StringFixed(6)),
	)
	return transfer.ID, nil
}

// waitForBridgeTransfer polls the Bridge transfer until it settles or fails.
func (w *Worker) waitForBridgeTransfer(ctx context.Context, transferID string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for transfer %s", transferID)
		case <-ticker.C:
			transfer, err := w.bridgeClient.GetTransfer(ctx, transferID)
			if err != nil {
				w.logger.Warn("Failed to poll bridge transfer", zap.String("id", transferID), zap.Error(err))
				continue
			}

			switch transfer.State {
			case bridge.TransferStatusPaymentProcessed:
				w.logger.Info("Bridge transfer settled", zap.String("id", transferID))
				return nil
			case bridge.TransferStatusPaymentSubmitted, bridge.TransferStatusFundsReceived:
				// Still in progress — keep polling.
				continue
			case bridge.TransferStatusAwaitingFunds, bridge.TransferStatusInReview:
				continue
			default:
				// Any terminal failure state.
				return fmt.Errorf("bridge transfer %s failed with state: %s", transferID, transfer.State)
			}
		}
	}
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

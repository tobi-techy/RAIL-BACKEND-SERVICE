package treasury_sweep

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/lulo"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// LedgerReader returns the aggregate stash balance across all users.
type LedgerReader interface {
	GetTotalStashBalance(ctx context.Context) (decimal.Decimal, error)
}

// Worker periodically sweeps aggregate stash USDC into the Lulo yield pool.
//
// Two-phase flow:
//  1. Transfer USDC from Bridge custody wallet → Rail's Solana wallet (via Bridge API)
//  2. Deposit USDC from Solana wallet → Lulo pool (via Lulo tx on Solana)
type Worker struct {
	lulo               *lulo.Client
	bridgeClient       *bridge.Client
	ledger             LedgerReader
	db                 *sqlx.DB
	bridgeCustomerID   string // Rail's Bridge customer ID
	bridgeSourceWallet string // Bridge wallet ID holding USDC to fund Solana wallet
	solanaWallet       string // Rail's Solana wallet address (destination for Bridge transfer)
	poolType           string
	minSweepAmount     decimal.Decimal
	interval           time.Duration
	logger             *zap.Logger
	stopCh             chan struct{}
	sweeping           int32
}

// NewWorker creates a treasury sweep worker.
func NewWorker(
	luloClient *lulo.Client,
	bridgeClient *bridge.Client,
	ledger LedgerReader,
	db *sqlx.DB,
	bridgeCustomerID string,
	bridgeSourceWallet string,
	solanaWallet string,
	poolType string,
	minSweepAmount decimal.Decimal,
	interval time.Duration,
	logger *zap.Logger,
) *Worker {
	if poolType != "regular" && poolType != "protected" {
		poolType = "protected"
		logger.Warn("Invalid pool type, defaulting to protected", zap.String("pool_type", poolType))
	}
	return &Worker{
		lulo:               luloClient,
		bridgeClient:       bridgeClient,
		ledger:             ledger,
		db:                 db,
		bridgeCustomerID:   bridgeCustomerID,
		bridgeSourceWallet: bridgeSourceWallet,
		solanaWallet:       solanaWallet,
		poolType:           poolType,
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
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				if err := w.sweep(ctx); err != nil {
					w.logger.Error("Treasury sweep failed", zap.Error(err))
				}
				cancel()
			case <-w.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop signals the worker to shut down.
func (w *Worker) Stop() {
	close(w.stopCh)
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

	account, err := w.lulo.GetAccount(ctx)
	if err != nil {
		return fmt.Errorf("get lulo account: %w", err)
	}

	diff := ledgerTotal.Sub(account.DepositedValue)

	w.logger.Info("Treasury sweep check",
		zap.String("ledger_total", ledgerTotal.StringFixed(6)),
		zap.String("lulo_deposited", account.DepositedValue.StringFixed(6)),
		zap.String("lulo_total_value", account.TotalValue.StringFixed(6)),
		zap.String("diff", diff.StringFixed(6)),
	)

	if diff.Abs().LessThan(w.minSweepAmount) {
		return nil
	}

	if diff.IsPositive() {
		return w.deposit(ctx, diff)
	}
	return w.withdraw(ctx, diff.Abs())
}

// deposit executes the two-phase flow: Bridge→Solana, then Solana→Lulo.
func (w *Worker) deposit(ctx context.Context, amount decimal.Decimal) error {
	// Phase 1: Transfer USDC from Bridge custody to Rail's Solana wallet.
	bridgeTxID, err := w.fundSolanaWallet(ctx, amount)
	if err != nil {
		return fmt.Errorf("fund solana wallet: %w", err)
	}

	// Wait for Bridge transfer to settle (Solana transfers are typically <30s).
	if err := w.waitForBridgeTransfer(ctx, bridgeTxID); err != nil {
		return fmt.Errorf("bridge transfer did not settle: %w", err)
	}

	// Phase 2: Deposit from Solana wallet into Lulo.
	txHash, err := w.lulo.Deposit(ctx, amount, w.poolType)
	if err != nil {
		return fmt.Errorf("lulo deposit: %w", err)
	}

	w.logger.Info("Treasury sweep deposit completed",
		zap.String("amount", amount.StringFixed(6)),
		zap.String("bridge_transfer", bridgeTxID),
		zap.String("lulo_tx", txHash),
	)
	return w.recordOperation(ctx, "deposit", amount, txHash)
}

func (w *Worker) withdraw(ctx context.Context, amount decimal.Decimal) error {
	txHash, err := w.lulo.Withdraw(ctx, amount, w.poolType)
	if err != nil {
		return fmt.Errorf("lulo withdraw: %w", err)
	}

	w.logger.Info("Treasury sweep withdrawal completed",
		zap.String("amount", amount.StringFixed(6)),
		zap.String("lulo_tx", txHash),
	)
	return w.recordOperation(ctx, "withdrawal", amount, txHash)
}

// fundSolanaWallet creates a Bridge transfer from the custody wallet to the Solana wallet.
func (w *Worker) fundSolanaWallet(ctx context.Context, amount decimal.Decimal) (string, error) {
	req := &bridge.CreateTransferRequest{
		ClientReferenceID: fmt.Sprintf("lulo-sweep-%s", uuid.New().String()[:8]),
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

func (w *Worker) recordOperation(ctx context.Context, opType string, amount decimal.Decimal, txHash string) error {
	_, err := w.db.ExecContext(ctx,
		`INSERT INTO treasury_positions (id, operation, amount, tx_hash) VALUES ($1, $2, $3, $4)`,
		uuid.New(), opType, amount, txHash,
	)
	if err != nil {
		w.logger.Error("Failed to record treasury operation (non-fatal)", zap.Error(err))
	}
	return nil
}

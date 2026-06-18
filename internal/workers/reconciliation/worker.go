package reconciliation

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// LedgerReader returns the sum of all stash_balance ledger accounts.
type LedgerReader interface {
	GetTotalStashBalance(ctx context.Context) (decimal.Decimal, error)
}

// BridgeWallet returns the balance held in the yield provider's custody.
type BridgeWallet interface {
	GetWalletBalance(ctx context.Context, customerID, walletID string) (decimal.Decimal, error)
}

// DistributedYieldReader returns the total yield ever distributed to users.
// Used to reconcile ledger stash (which includes distributed yield) against
// the yield-provider position (which holds only the original principal).
type DistributedYieldReader interface {
	GetTotalDistributedYield(ctx context.Context) (decimal.Decimal, error)
}

// Worker performs a daily reconciliation between the internal ledger stash total
// and the actual balance held in the yield provider (Blend).
type Worker struct {
	ledger           LedgerReader
	bridge           BridgeWallet
	distributedYield DistributedYieldReader
	customerID       string
	walletID         string
	logger           *zap.Logger
}

func NewWorker(ledger LedgerReader, bridge BridgeWallet, distributedYield DistributedYieldReader, customerID, walletID string, logger *zap.Logger) *Worker {
	return &Worker{ledger: ledger, bridge: bridge, distributedYield: distributedYield, customerID: customerID, walletID: walletID, logger: logger}
}

// Run compares sum(stash_balance) - total_distributed_yield in the ledger
// against the yield provider balance (depositedUSDC × rate).
// Any discrepancy is logged as an error for alerting. No auto-correction is performed.
func (w *Worker) Run(ctx context.Context) error {
	ledgerTotal, err := w.ledger.GetTotalStashBalance(ctx)
	if err != nil {
		return fmt.Errorf("reconciliation: get ledger stash total: %w", err)
	}

	totalDistributed, err := w.distributedYield.GetTotalDistributedYield(ctx)
	if err != nil {
		return fmt.Errorf("reconciliation: get total distributed yield: %w", err)
	}

	// Ledger stash includes yield already credited to users.
	// The yield-provider position holds only the original principal.
	// Subtract distributed yield to make them comparable.
	ledgerPrincipal := ledgerTotal.Sub(totalDistributed)

	bridgeBalance, err := w.bridge.GetWalletBalance(ctx, w.customerID, w.walletID)
	if err != nil {
		return fmt.Errorf("reconciliation: get yield provider balance: %w", err)
	}

	diff := ledgerPrincipal.Sub(bridgeBalance).Abs()
	fields := []zap.Field{
		zap.String("ledger_stash_total", ledgerTotal.StringFixed(6)),
		zap.String("total_distributed_yield", totalDistributed.StringFixed(6)),
		zap.String("ledger_principal", ledgerPrincipal.StringFixed(6)),
		zap.String("provider_balance", bridgeBalance.StringFixed(6)),
		zap.String("discrepancy", diff.StringFixed(6)),
	}

	if diff.IsZero() {
		w.logger.Info("Reconciliation OK: ledger principal matches yield provider", fields...)
		return nil
	}

	w.logger.Error("RECONCILIATION MISMATCH: ledger principal does not match yield provider balance", fields...)
	return fmt.Errorf("reconciliation mismatch: ledger_principal=%s provider=%s diff=%s",
		ledgerPrincipal.StringFixed(6), bridgeBalance.StringFixed(6), diff.StringFixed(6))
}

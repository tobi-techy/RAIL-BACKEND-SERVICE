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

// BridgeWallet returns the USDB balance held in Rail's Bridge custody wallet.
type BridgeWallet interface {
	GetWalletBalance(ctx context.Context, customerID, walletID string) (decimal.Decimal, error)
}

// Worker performs a daily reconciliation between the internal ledger stash total
// and the actual USDB balance held in Bridge custody.
type Worker struct {
	ledger     LedgerReader
	bridge     BridgeWallet
	customerID string
	walletID   string
	logger     *zap.Logger
}

func NewWorker(ledger LedgerReader, bridge BridgeWallet, customerID, walletID string, logger *zap.Logger) *Worker {
	return &Worker{ledger: ledger, bridge: bridge, customerID: customerID, walletID: walletID, logger: logger}
}

// Run compares sum(stash_balance) in the ledger against the Bridge USDB wallet balance.
// Any discrepancy is logged as an error for alerting. No auto-correction is performed.
func (w *Worker) Run(ctx context.Context) error {
	ledgerTotal, err := w.ledger.GetTotalStashBalance(ctx)
	if err != nil {
		return fmt.Errorf("reconciliation: get ledger stash total: %w", err)
	}

	bridgeBalance, err := w.bridge.GetWalletBalance(ctx, w.customerID, w.walletID)
	if err != nil {
		return fmt.Errorf("reconciliation: get bridge usdb balance: %w", err)
	}

	diff := ledgerTotal.Sub(bridgeBalance).Abs()
	fields := []zap.Field{
		zap.String("ledger_stash_total", ledgerTotal.StringFixed(6)),
		zap.String("bridge_usdb_balance", bridgeBalance.StringFixed(6)),
		zap.String("discrepancy", diff.StringFixed(6)),
	}

	if diff.IsZero() {
		w.logger.Info("Reconciliation OK: ledger matches Bridge", fields...)
		return nil
	}

	// Any discrepancy is an error — alert immediately.
	w.logger.Error("RECONCILIATION MISMATCH: ledger stash total does not match Bridge USDB balance", fields...)
	return fmt.Errorf("reconciliation mismatch: ledger=%s bridge=%s diff=%s",
		ledgerTotal.StringFixed(6), bridgeBalance.StringFixed(6), diff.StringFixed(6))
}

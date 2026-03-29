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

// Worker performs a daily reconciliation between the internal ledger stash total
// and the actual balance held in the yield provider (Lulo).
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

// Run compares sum(stash_balance) in the ledger against the yield provider balance.
// Any discrepancy is logged as an error for alerting. No auto-correction is performed.
func (w *Worker) Run(ctx context.Context) error {
	ledgerTotal, err := w.ledger.GetTotalStashBalance(ctx)
	if err != nil {
		return fmt.Errorf("reconciliation: get ledger stash total: %w", err)
	}

	bridgeBalance, err := w.bridge.GetWalletBalance(ctx, w.customerID, w.walletID)
	if err != nil {
		return fmt.Errorf("reconciliation: get yield provider balance: %w", err)
	}

	diff := ledgerTotal.Sub(bridgeBalance).Abs()
	fields := []zap.Field{
		zap.String("ledger_stash_total", ledgerTotal.StringFixed(6)),
		zap.String("provider_balance", bridgeBalance.StringFixed(6)),
		zap.String("discrepancy", diff.StringFixed(6)),
	}

	if diff.IsZero() {
		w.logger.Info("Reconciliation OK: ledger matches yield provider", fields...)
		return nil
	}

	// Any discrepancy is an error — alert immediately.
	w.logger.Error("RECONCILIATION MISMATCH: ledger stash total does not match yield provider balance", fields...)
	return fmt.Errorf("reconciliation mismatch: ledger=%s provider=%s diff=%s",
		ledgerTotal.StringFixed(6), bridgeBalance.StringFixed(6), diff.StringFixed(6))
}

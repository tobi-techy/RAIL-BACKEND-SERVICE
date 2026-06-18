package di

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
)

// blendReconciliationAdapter adapts the Blend yield tables to reconciliation.BridgeWallet.
//
// The reconciliation invariant is: ledger_principal == blend_principal_held, where
// blend_principal_held = settled position principal (net of redemptions) plus the
// principal of in-flight deposit routes that have not yet settled. Including in-flight
// routes avoids false mismatches during the brief window between ledger allocation and
// on-chain settlement.
type blendReconciliationAdapter struct {
	db *sqlx.DB
}

func (a *blendReconciliationAdapter) GetWalletBalance(ctx context.Context, _, _ string) (decimal.Decimal, error) {
	var settled decimal.Decimal
	if err := a.db.GetContext(ctx, &settled, `
		SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
		FROM blend_yield_positions WHERE status = 'active'
	`); err != nil {
		return decimal.Zero, fmt.Errorf("blendReconciliationAdapter: read positions: %w", err)
	}
	var inflight decimal.Decimal
	if err := a.db.GetContext(ctx, &inflight, `
		SELECT COALESCE(SUM(amount), 0)
		FROM blend_deposit_routes
		WHERE status NOT IN ('complete', 'error_terminal', 'error_payload')
	`); err != nil {
		return decimal.Zero, fmt.Errorf("blendReconciliationAdapter: read in-flight routes: %w", err)
	}
	return settled.Add(inflight), nil
}

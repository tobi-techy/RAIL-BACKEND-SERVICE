package di

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	yieldsvc "github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/reflect"
	"github.com/shopspring/decimal"
)

// reflectRewardsAdapter adapts reflect.Client to yieldsvc.RewardsProvider.
//
// Yield delta = deposited_usdc × (current_rate - last_rate)
// where rate = base_usd_value_bps / 10000 (e.g. 10043 bps → 1.0043)
// The high-water mark stored in yield_state key 'last_exchange_rate' is the decimal rate.
// Migration 137 seeds it at 1.0000 (= 10000 bps).
type reflectRewardsAdapter struct {
	client *reflect.Client
	db     *sqlx.DB
}

func (a *reflectRewardsAdapter) GetRewardsSummary(ctx context.Context, currency string, currentRate decimal.Decimal) (*yieldsvc.RewardSummary, error) {
	// Read both state values atomically in one query to avoid a race with the sweep worker.
	rows, err := a.db.QueryContext(ctx,
		`SELECT key, value FROM yield_state WHERE key IN ('last_exchange_rate', 'reflect_deposited_usdc')`)
	if err != nil {
		return nil, fmt.Errorf("reflectRewardsAdapter: read yield_state: %w", err)
	}
	defer rows.Close()

	state := make(map[string]decimal.Decimal, 2)
	for rows.Next() {
		var key string
		var val decimal.Decimal
		if err := rows.Scan(&key, &val); err != nil {
			return nil, fmt.Errorf("reflectRewardsAdapter: scan yield_state: %w", err)
		}
		state[key] = val
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reflectRewardsAdapter: yield_state rows: %w", err)
	}

	lastRate, ok1 := state["last_exchange_rate"]
	depositedUSDC, ok2 := state["reflect_deposited_usdc"]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("reflectRewardsAdapter: missing yield_state keys (run migration 137)")
	}

	rateDelta := currentRate.Sub(lastRate)
	if rateDelta.IsNegative() {
		rateDelta = decimal.Zero
	}

	newYield := depositedUSDC.Mul(rateDelta)
	return &yieldsvc.RewardSummary{Rewards: newYield.StringFixed(6)}, nil
}

// AdvanceExchangeRateMark persists the new exchange rate high-water mark after a successful distribution.
// Call this ONLY after RunDistribution completes without error.
// reflect_deposited_usdc is intentionally NOT updated here — it tracks raw USDC principal only.
// The reconciliation adapter accounts for distributed yield separately.
func AdvanceExchangeRateMark(ctx context.Context, db *sqlx.DB, newRate decimal.Decimal, _ decimal.Decimal) error {
	res, err := db.ExecContext(ctx,
		`UPDATE yield_state SET value = $1, updated_at = NOW() WHERE key = 'last_exchange_rate'`,
		newRate)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("AdvanceExchangeRateMark: yield_state row 'last_exchange_rate' not found — run migration 137")
	}
	return nil
}


// reflectReconciliationAdapter adapts yield_state to reconciliation.BridgeWallet.
// Returns raw reflect_deposited_usdc — the invariant is ledger_principal == deposited_usdc.
type reflectReconciliationAdapter struct {
	db *sqlx.DB
}

func (a *reflectReconciliationAdapter) GetWalletBalance(ctx context.Context, customerID, walletID string) (decimal.Decimal, error) {
	// The reconciliation invariant is: ledger_principal == reflect_deposited_usdc
	// where ledger_principal = sum(stash_balances) - sum(distributed_yield)
	// reflect_deposited_usdc is the raw USDC deposited (principal only, no rate appreciation).
	// Yield appreciation is unrealised until distributed — it is NOT a discrepancy.
	var depositedUSDC decimal.Decimal
	if err := a.db.GetContext(ctx, &depositedUSDC,
		`SELECT value FROM yield_state WHERE key = 'reflect_deposited_usdc'`); err != nil {
		return decimal.Zero, fmt.Errorf("reflectReconciliationAdapter: read deposited usdc: %w", err)
	}
	return depositedUSDC, nil
}

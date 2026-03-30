package di

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	yieldsvc "github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/lulo"
	"github.com/shopspring/decimal"
)

// luloRewardsAdapter adapts lulo.Client to yieldsvc.RewardsProvider.
// Returns the delta between Lulo's cumulative InterestEarned and the persisted
// high-water mark. The mark is NOT advanced here — it must be advanced by the
// caller after successful distribution (see yield_distribution worker).
type luloRewardsAdapter struct {
	client *lulo.Client
	db     *sqlx.DB
}

func (a *luloRewardsAdapter) GetRewardsSummary(ctx context.Context, currency string) (*yieldsvc.RewardSummary, error) {
	account, err := a.client.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("luloRewardsAdapter: get account: %w", err)
	}

	var lastDistributed decimal.Decimal
	err = a.db.GetContext(ctx, &lastDistributed,
		`SELECT value FROM yield_state WHERE key = 'last_distributed_yield'`)
	if err != nil {
		return nil, fmt.Errorf("luloRewardsAdapter: read high-water mark: %w", err)
	}

	newYield := account.InterestEarned.Sub(lastDistributed)
	if newYield.IsNegative() {
		newYield = decimal.Zero
	}

	return &yieldsvc.RewardSummary{Rewards: newYield.StringFixed(6)}, nil
}

// AdvanceYieldMark persists the new high-water mark after a successful distribution.
// Call this ONLY after RunDistribution completes without error.
func AdvanceYieldMark(ctx context.Context, db *sqlx.DB, newMark decimal.Decimal) error {
	_, err := db.ExecContext(ctx,
		`UPDATE yield_state SET value = $1, updated_at = NOW() WHERE key = 'last_distributed_yield'`,
		newMark)
	return err
}

// GetCurrentLuloInterest returns the current cumulative interest from Lulo.
// Used by the yield_distribution worker to know what value to advance the mark to.
func GetCurrentLuloInterest(ctx context.Context, client *lulo.Client) (decimal.Decimal, error) {
	account, err := client.GetAccount(ctx)
	if err != nil {
		return decimal.Zero, err
	}
	return account.InterestEarned, nil
}

// luloReconciliationAdapter adapts lulo.Client to reconciliation.BridgeWallet.
type luloReconciliationAdapter struct {
	client *lulo.Client
}

func (a *luloReconciliationAdapter) GetWalletBalance(ctx context.Context, customerID, walletID string) (decimal.Decimal, error) {
	account, err := a.client.GetAccount(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("luloReconciliationAdapter: get account: %w", err)
	}
	return account.DepositedValue, nil
}

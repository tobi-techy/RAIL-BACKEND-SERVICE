package blend

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserYieldOverview is the frontend-facing snapshot of a user's Blend position.
type UserYieldOverview struct {
	Active        bool            `json:"active"`
	AccountID     string          `json:"account_id,omitempty"`
	SafeAddress   string          `json:"safe_address,omitempty"`
	ChainID       int64           `json:"chain_id"`
	Principal     decimal.Decimal `json:"principal"`      // net deposited still held (DB ledger)
	CurrentValue  decimal.Decimal `json:"current_value"`  // current underlying value (Blend)
	YieldEarned   decimal.Decimal `json:"yield_earned"`   // current - net deposited (Blend returns)
	ReturnsPct    float64         `json:"returns_pct"`    // lifetime % return
	PendingAmount decimal.Decimal `json:"pending_amount"` // stash routed but not yet settled
}

// GetUserYieldOverview returns a read-only snapshot for the authenticated user.
// It never mutates state and degrades gracefully if the Blend API is unavailable
// (falling back to DB-tracked principal).
func (r *DepositRouter) GetUserYieldOverview(ctx context.Context, userID uuid.UUID) (*UserYieldOverview, error) {
	out := &UserYieldOverview{ChainID: r.chainID, Principal: decimal.Zero, CurrentValue: decimal.Zero, YieldEarned: decimal.Zero, PendingAmount: decimal.Zero}

	acct, err := r.getUserAccount(ctx, userID)
	if err != nil {
		return nil, err
	}

	// DB-tracked principal (settled positions) and pending (in-flight routes).
	if perr := r.db.GetContext(ctx, &out.Principal, `
		SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
		FROM blend_yield_positions WHERE user_id = $1 AND status = 'active'
	`, userID); perr != nil {
		return nil, perr
	}
	if perr := r.db.GetContext(ctx, &out.PendingAmount, `
		SELECT COALESCE(SUM(amount), 0)
		FROM blend_deposit_routes
		WHERE user_id = $1 AND status NOT IN ('complete', 'error_terminal', 'error_payload')
	`, userID); perr != nil {
		return nil, perr
	}

	if acct == nil || acct.BlendAccountID == "" {
		out.CurrentValue = out.Principal
		return out, nil
	}
	out.Active = true
	out.AccountID = acct.BlendAccountID
	out.SafeAddress = acct.SafeAddress

	// Live values from Blend. Best-effort: on API error, fall back to DB principal.
	if balance, berr := r.blend.GetBalance(ctx, acct.BlendAccountID); berr == nil {
		out.CurrentValue = aggregateUnderlying(balance)
	} else {
		out.CurrentValue = out.Principal
		r.logger.Warn("blend: GetBalance failed for overview; using DB principal", zap.Error(berr))
	}
	if rets, rerr := r.blend.GetReturns(ctx, acct.BlendAccountID); rerr == nil {
		out.ReturnsPct = rets.ReturnsPct
		out.YieldEarned = pickCurrency(rets.ReturnsAmount)
	} else {
		out.YieldEarned = out.CurrentValue.Sub(out.Principal)
	}
	return out, nil
}

func aggregateUnderlying(b *Balance) decimal.Decimal {
	if b == nil {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, pc := range b.PerChain {
		if pc.TotalUnderlying == "" || pc.TotalUnderlyingDecimals <= 0 {
			continue
		}
		if units, ok := parseBig(pc.TotalUnderlying); ok {
			total = total.Add(decimal.NewFromBigInt(units, -int32(pc.TotalUnderlyingDecimals)))
		}
	}
	return total
}

func pickCurrency(m map[string]float64) decimal.Decimal {
	// Prefer USDC/USD, else first value.
	for _, k := range []string{"USDC", "USD", "usd", "usdc"} {
		if v, ok := m[k]; ok {
			return decimal.NewFromFloat(v)
		}
	}
	for _, v := range m {
		return decimal.NewFromFloat(v)
	}
	return decimal.Zero
}

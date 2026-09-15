-- Per-user investment limit overrides.
-- The investment (Glider) schema adds assets, strategies, portfolios, ledger
-- and audit tables, but has no home for per-user limit overrides; the domain
-- LimitsRepository requires one. Nullable-by-absence: a missing row means the
-- deployment default limits apply.

CREATE TABLE IF NOT EXISTS investment_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    max_position_pct NUMERIC(9, 4) NOT NULL DEFAULT 0,
    max_strategy_pct NUMERIC(9, 4) NOT NULL DEFAULT 0,
    max_transaction_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    max_daily_volume_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    min_cash_reserve_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    min_order_amount_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    max_enrollments INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Migration 290 created an aggregate table that older environments may already have.
-- Recreate that table from the historical 290 SQL so this branch can safely rely on
-- the explicit schema while still preserving the migration history.
DROP TABLE IF EXISTS conscious_spending_plans;

CREATE TABLE conscious_spending_plans (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    take_home_income NUMERIC(20, 4) NOT NULL CHECK (take_home_income > 0),
    currency VARCHAR(10) NOT NULL,
    fixed_costs NUMERIC(20, 4) NOT NULL CHECK (fixed_costs >= 0),
    investments NUMERIC(20, 4) NOT NULL CHECK (investments >= 0),
    savings NUMERIC(20, 4) NOT NULL CHECK (savings >= 0),
    guilt_free_spending NUMERIC(20, 4) NOT NULL CHECK (guilt_free_spending >= 0),
    fixed_costs_pct NUMERIC(7, 4) NOT NULL CHECK (fixed_costs_pct >= 0),
    investments_pct NUMERIC(7, 4) NOT NULL CHECK (investments_pct >= 0),
    savings_pct NUMERIC(7, 4) NOT NULL CHECK (savings_pct >= 0),
    guilt_free_spending_pct NUMERIC(7, 4) NOT NULL CHECK (guilt_free_spending_pct >= 0),
    status VARCHAR(20) NOT NULL
        CHECK (status IN ('committed', 'paused')),
    check_in_cadence VARCHAR(20) NOT NULL DEFAULT 'weekly'
        CHECK (check_in_cadence IN ('weekly', 'biweekly', 'monthly')),
    committed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS conscious_spending_plans_committed_idx
    ON conscious_spending_plans (updated_at)
    WHERE status = 'committed';

COMMENT ON TABLE conscious_spending_plans IS
    'Legacy aggregate Conscious Spending Plan rows preserved for migration safety.';
COMMENT ON COLUMN conscious_spending_plans.investments IS
    'Historical aggregate investment bucket; new semantic detail lives in conscious_spending_plan_versions.';

-- Versioned planning surface.
CREATE TABLE conscious_spending_plan_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version INT NOT NULL,
    scope VARCHAR(30) NOT NULL DEFAULT 'household',
    country VARCHAR(60),
    base_currency VARCHAR(10) NOT NULL,
    gross_monthly_income NUMERIC(20, 4) NOT NULL CHECK (gross_monthly_income >= 0),
    payroll_deductions NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (payroll_deductions >= 0),
    pre_tax_investments NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (pre_tax_investments >= 0),
    take_home_income NUMERIC(20, 4) NOT NULL CHECK (take_home_income >= 0),
    income_cadence VARCHAR(20) NOT NULL DEFAULT 'monthly',
    income_basis VARCHAR(40) NOT NULL DEFAULT 'user_provided',
    income_source VARCHAR(60),
    income_confidence VARCHAR(20) NOT NULL DEFAULT 'low',
    fixed_costs_subtotal NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (fixed_costs_subtotal >= 0),
    misc_buffer_rate NUMERIC(7, 4) NOT NULL DEFAULT 0.15 CHECK (misc_buffer_rate >= 0 AND misc_buffer_rate <= 1),
    misc_buffer_amount NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (misc_buffer_amount >= 0),
    fixed_costs NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (fixed_costs >= 0),
    post_tax_investments NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (post_tax_investments >= 0),
    savings NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (savings >= 0),
    guilt_free_spending NUMERIC(20, 4) NOT NULL DEFAULT 0 CHECK (guilt_free_spending >= 0),
    fixed_costs_pct NUMERIC(7, 4) NOT NULL DEFAULT 0,
    investments_pct NUMERIC(7, 4) NOT NULL DEFAULT 0,
    savings_pct NUMERIC(7, 4) NOT NULL DEFAULT 0,
    guilt_free_spending_pct NUMERIC(7, 4) NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'committed'
        CHECK (status IN ('committed', 'superseded', 'paused')),
    check_in_cadence VARCHAR(20) NOT NULL DEFAULT 'weekly'
        CHECK (check_in_cadence IN ('weekly', 'biweekly', 'monthly')),
    committed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        ABS(
            take_home_income -
            (fixed_costs + misc_buffer_amount + post_tax_investments + savings + guilt_free_spending)
        ) <= 0.01
    )
);

CREATE TABLE conscious_spending_plan_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES conscious_spending_plan_versions(id) ON DELETE CASCADE,
    bucket VARCHAR(20) NOT NULL CHECK (bucket IN ('fixed_cost', 'investment', 'savings')),
    name VARCHAR(200) NOT NULL,
    amount NUMERIC(20, 4) NOT NULL CHECK (amount >= 0),
    cadence VARCHAR(20) NOT NULL DEFAULT 'monthly',
    source VARCHAR(60) NOT NULL DEFAULT 'user_provided',
    confidence VARCHAR(20) NOT NULL DEFAULT 'low',
    original_amount NUMERIC(20, 4),
    original_currency VARCHAR(10),
    fx_rate NUMERIC(20, 8),
    fx_rate_at TIMESTAMPTZ,
    evidence_ref TEXT,
    display_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS conscious_spending_plan_versions_user_idx
    ON conscious_spending_plan_versions (user_id, version DESC);

CREATE INDEX IF NOT EXISTS conscious_spending_plan_versions_committed_idx
    ON conscious_spending_plan_versions (updated_at)
    WHERE status = 'committed';

CREATE INDEX IF NOT EXISTS conscious_spending_plan_items_plan_idx
    ON conscious_spending_plan_items (plan_id, display_order, created_at);

CREATE TABLE conscious_spending_net_worth_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID REFERENCES conscious_spending_plan_versions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    currency VARCHAR(10) NOT NULL,
    assets NUMERIC(20, 4) NOT NULL DEFAULT 0,
    investments NUMERIC(20, 4) NOT NULL DEFAULT 0,
    savings NUMERIC(20, 4) NOT NULL DEFAULT 0,
    debt NUMERIC(20, 4) NOT NULL DEFAULT 0,
    total NUMERIC(20, 4) NOT NULL DEFAULT 0,
    source VARCHAR(60) NOT NULL DEFAULT 'user_provided',
    confidence VARCHAR(20) NOT NULL DEFAULT 'low',
    captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS conscious_spending_net_worth_user_idx
    ON conscious_spending_net_worth_snapshots (user_id, captured_at DESC);

COMMENT ON TABLE conscious_spending_plan_versions IS
    'Versioned household Conscious Spending Plans. The previous aggregate table is preserved under migration 290; use this table for full semantics and history.';

COMMENT ON TABLE conscious_spending_plan_items IS
    'Line-item evidence for recurring commitments, investments, and savings goals. Original-currency/FX fields preserve cross-border provenance.';

COMMENT ON TABLE conscious_spending_net_worth_snapshots IS
    'User-reported or connected net-worth snapshots referenced during planning and coaching.';

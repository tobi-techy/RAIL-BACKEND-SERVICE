CREATE TABLE IF NOT EXISTS conscious_spending_plans (
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
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        ABS(
            take_home_income -
            (fixed_costs + investments + savings + guilt_free_spending)
        ) <= 0.01
    )
);

CREATE INDEX IF NOT EXISTS conscious_spending_plans_committed_idx
    ON conscious_spending_plans (updated_at)
    WHERE status = 'committed';

COMMENT ON TABLE conscious_spending_plans IS
    'User-approved Conscious Spending Plans. Personal motivations remain in Miriam memory; this table stores only the numeric coaching commitment.';

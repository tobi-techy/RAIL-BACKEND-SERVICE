-- Self-imposed daily spending commitment: a user-set daily cap on total outflows.
-- Lowering the cap is free; raising it charges a flat fee. Enforced across card,
-- withdrawal, and P2P outflows via a unified daily usage counter (USD cents).

CREATE TABLE IF NOT EXISTS spending_commitments (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    daily_limit_cents BIGINT NOT NULL CHECK (daily_limit_cents > 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    increase_count INTEGER NOT NULL DEFAULT 0,
    last_increased_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS spending_commitment_daily_usage (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    used_cents BIGINT NOT NULL DEFAULT 0 CHECK (used_cents >= 0),
    reset_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE yield_balance_snapshots (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    balance     NUMERIC(20,6) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ybs_user_recorded ON yield_balance_snapshots(user_id, recorded_at);

CREATE TABLE yield_distributions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_start      TIMESTAMPTZ NOT NULL,
    period_end        TIMESTAMPTZ NOT NULL,
    total_reward      NUMERIC(20,6) NOT NULL,
    total_twb         NUMERIC(30,6) NOT NULL DEFAULT 0,
    total_distributed NUMERIC(20,6) NOT NULL DEFAULT 0,
    remainder         NUMERIC(20,6) NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'pending',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(period_start, period_end)
);

CREATE TABLE yield_distribution_users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    distribution_id UUID NOT NULL REFERENCES yield_distributions(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    twb             NUMERIC(30,6) NOT NULL,
    share_pct       NUMERIC(10,8) NOT NULL,
    reward_amount   NUMERIC(20,6) NOT NULL,
    credited_at     TIMESTAMPTZ,
    UNIQUE(distribution_id, user_id)
);
CREATE INDEX idx_ydu_distribution ON yield_distribution_users(distribution_id);

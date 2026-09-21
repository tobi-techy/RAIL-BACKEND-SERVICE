-- Cost-basis lots, valuation snapshots, penalty events and single-use
-- withdrawal authorizations for the retirement vault.

-- One row per contribution: the cost basis. Consumed FIFO on withdrawal.
CREATE TABLE IF NOT EXISTS vault_contribution_lots (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id              UUID NOT NULL REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_usd            NUMERIC(36,18) NOT NULL CHECK (amount_usd > 0),
    remaining_usd         NUMERIC(36,18) NOT NULL CHECK (remaining_usd >= 0),
    source_account        TEXT,
    ledger_transaction_id UUID,
    funding_transfer_id   UUID,
    onramp_transfer_id    UUID,
    ngn_amount            NUMERIC(36,18),
    fx_rate               NUMERIC(36,18),
    acquired_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status                TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'consumed')),
    idempotency_key       TEXT UNIQUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vault_lots_vault_open
    ON vault_contribution_lots(vault_id, acquired_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_vault_lots_user ON vault_contribution_lots(user_id);

-- Point-in-time principal/earnings valuation, written after each provider sync.
CREATE TABLE IF NOT EXISTS vault_earnings_snapshots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id         UUID NOT NULL REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    principal_usd    NUMERIC(36,18) NOT NULL DEFAULT 0,
    earnings_usd     NUMERIC(36,18) NOT NULL DEFAULT 0,
    market_value_usd NUMERIC(36,18) NOT NULL DEFAULT 0,
    source           TEXT NOT NULL DEFAULT 'glider_sync',
    as_of            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vault_snapshots_vault_asof
    ON vault_earnings_snapshots(vault_id, as_of DESC);

-- The 10% haircut, recorded pending at authorisation and committed at settlement.
CREATE TABLE IF NOT EXISTS vault_penalty_events (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id               UUID NOT NULL REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    withdrawal_id          UUID,
    execution_id           UUID,
    earnings_withdrawn_usd NUMERIC(36,18) NOT NULL DEFAULT 0,
    penalty_usd            NUMERIC(36,18) NOT NULL DEFAULT 0,
    rate                   NUMERIC(6,5) NOT NULL DEFAULT 0.10,
    status                 TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'committed', 'void')),
    reason                 TEXT,
    ledger_transaction_id  UUID,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vault_penalties_vault ON vault_penalty_events(vault_id);
CREATE INDEX IF NOT EXISTS idx_vault_penalties_execution ON vault_penalty_events(execution_id);

-- Single-use capability minted by the vault and consumed by the withdrawal gate.
CREATE TABLE IF NOT EXISTS vault_withdrawal_authorizations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id      UUID NOT NULL REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID NOT NULL,
    key           TEXT NOT NULL UNIQUE,
    gross_usd     NUMERIC(36,18) NOT NULL,
    penalty_usd   NUMERIC(36,18) NOT NULL DEFAULT 0,
    net_usd       NUMERIC(36,18) NOT NULL,
    plan          JSONB,
    status        TEXT NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'consumed', 'expired')),
    execution_id  UUID,
    expires_at    TIMESTAMPTZ NOT NULL,
    consumed_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vault_auth_enrollment ON vault_withdrawal_authorizations(enrollment_id, status);

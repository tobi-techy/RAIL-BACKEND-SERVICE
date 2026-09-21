-- NGN→USD conversion audit for retirement contributions, so every Naira unit
-- is reconcilable: NGN in → USDC settled → position.

CREATE TABLE IF NOT EXISTS vault_onramp_transfers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vault_id        UUID REFERENCES retirement_vaults(id) ON DELETE SET NULL,
    provider        TEXT NOT NULL CHECK (provider IN ('graph', 'ramphub', 'paj', 'yellowcard', 'kotani')),
    direction       TEXT NOT NULL DEFAULT 'onramp',
    ngn_amount      NUMERIC(36,18) NOT NULL DEFAULT 0,
    fx_rate         NUMERIC(36,18) NOT NULL DEFAULT 0,
    usdc_amount     NUMERIC(36,18) NOT NULL DEFAULT 0,
    provider_ref    TEXT,
    circle_tx_ref   TEXT,
    status          TEXT NOT NULL DEFAULT 'pending',
    idempotency_key TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_vault_onramp_user ON vault_onramp_transfers(user_id);
CREATE INDEX IF NOT EXISTS idx_vault_onramp_vault ON vault_onramp_transfers(vault_id);

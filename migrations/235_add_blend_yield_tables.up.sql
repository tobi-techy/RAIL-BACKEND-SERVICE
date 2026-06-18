-- Blend.money yield integration.
-- Replaces Reflect for new deposits while existing Reflect positions continue
-- to be redeemed by the existing reflect_deposit_routes pipeline.

CREATE TABLE IF NOT EXISTS blend_user_accounts (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id             UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    eoa_address         VARCHAR(42) NOT NULL,
    blend_account_id    VARCHAR(64) NOT NULL,
    safe_address        VARCHAR(42),
    chain_id            INTEGER NOT NULL DEFAULT 8453,
    safe_status         VARCHAR(32) NOT NULL DEFAULT 'unknown',
    safe_requested_at   TIMESTAMPTZ,
    safe_deployed_at    TIMESTAMPTZ,
    circle_wallet_id    VARCHAR(255) NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_blend_user_accounts_eoa
    ON blend_user_accounts(LOWER(eoa_address));

CREATE INDEX IF NOT EXISTS idx_blend_user_accounts_safe_status
    ON blend_user_accounts(safe_status);

CREATE TABLE IF NOT EXISTS blend_deposit_routes (
    id                  UUID PRIMARY KEY,
    deposit_id          UUID NOT NULL UNIQUE REFERENCES deposits(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blend_account_id    VARCHAR(64) NOT NULL,
    eoa_address         VARCHAR(42) NOT NULL,
    safe_address        VARCHAR(42),
    circle_wallet_id    VARCHAR(255) NOT NULL,   -- user's BASE Circle wallet (Safe owner / deposit signer)
    chain_id            INTEGER NOT NULL DEFAULT 8453,
    input_asset         VARCHAR(42) NOT NULL,
    amount              NUMERIC(36,18) NOT NULL,
    amount_units        NUMERIC(78,0),
    -- Funding source: where the stash USDC physically sits before it is bridged to Base.
    source_circle_wallet_id VARCHAR(255),         -- deposit wallet (may be Solana/EVM)
    source_chain            VARCHAR(32),          -- Circle blockchain of the source wallet
    -- ChainRails bridge tracking (source chain -> Base).
    bridge_intent_id        INTEGER,
    bridge_intent_address   VARCHAR(255),
    bridge_source_chain     VARCHAR(64),
    bridge_fund_amount      NUMERIC(36,18),
    bridge_tx_hash          VARCHAR(255),
    funded_at               TIMESTAMPTZ,
    intent_id           VARCHAR(64),
    intent_status       VARCHAR(32),
    intent_expires_at   TIMESTAMPTZ,
    quote_payload       JSONB,
    quote_summary       JSONB,
    tx_hash             VARCHAR(66),
    submitted_at        TIMESTAMPTZ,
    settled_at          TIMESTAMPTZ,
    status              VARCHAR(40) NOT NULL DEFAULT 'pending',
    attempts            INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT,
    next_retry_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    external_ref        VARCHAR(255) UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_blend_deposit_routes_status_retry
    ON blend_deposit_routes(status, next_retry_at);

CREATE INDEX IF NOT EXISTS idx_blend_deposit_routes_user
    ON blend_deposit_routes(user_id);

CREATE INDEX IF NOT EXISTS idx_blend_deposit_routes_intent
    ON blend_deposit_routes(intent_id) WHERE intent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS blend_yield_positions (
    id                  UUID PRIMARY KEY,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    route_id            UUID NOT NULL UNIQUE REFERENCES blend_deposit_routes(id) ON DELETE CASCADE,
    deposit_id          UUID NOT NULL REFERENCES deposits(id) ON DELETE CASCADE,
    blend_account_id    VARCHAR(64) NOT NULL,
    safe_address        VARCHAR(42) NOT NULL,
    chain_id            INTEGER NOT NULL DEFAULT 8453,
    principal_amount    NUMERIC(36,18) NOT NULL,
    redeemed_amount     NUMERIC(36,18) NOT NULL DEFAULT 0,
    deposit_tx_hash     VARCHAR(66) NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_blend_yield_positions_user_status
    ON blend_yield_positions(user_id, status);

CREATE TABLE IF NOT EXISTS blend_yield_redemptions (
    id                      UUID PRIMARY KEY,
    user_id                 UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blend_account_id        VARCHAR(64) NOT NULL,
    amount                  NUMERIC(36,18) NOT NULL,
    destination_chain_id    INTEGER NOT NULL DEFAULT 8453,
    intent_id               VARCHAR(64),
    intent_status           VARCHAR(32),
    quote_payload           JSONB,
    tx_hash                 VARCHAR(66),
    submitted_at            TIMESTAMPTZ,
    settled_at              TIMESTAMPTZ,
    idempotency_key         VARCHAR(255) NOT NULL UNIQUE,
    status                  VARCHAR(40) NOT NULL DEFAULT 'pending',
    attempts                INTEGER NOT NULL DEFAULT 0,
    last_error              TEXT,
    next_retry_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_blend_yield_redemptions_user
    ON blend_yield_redemptions(user_id);

CREATE INDEX IF NOT EXISTS idx_blend_yield_redemptions_status_retry
    ON blend_yield_redemptions(status, next_retry_at);

-- Snapshot Blend balance over time for analytics + reconciliation.
CREATE TABLE IF NOT EXISTS blend_balance_snapshots (
    id                          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blend_account_id            VARCHAR(64) NOT NULL,
    total_underlying            NUMERIC(36,18) NOT NULL,
    total_underlying_decimals   INTEGER NOT NULL DEFAULT 6,
    primary_symbol              VARCHAR(16) NOT NULL DEFAULT 'USDC',
    per_chain                   JSONB,
    recorded_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_blend_balance_snapshots_user_time
    ON blend_balance_snapshots(user_id, recorded_at DESC);

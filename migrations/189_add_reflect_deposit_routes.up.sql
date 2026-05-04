CREATE TABLE IF NOT EXISTS reflect_deposit_routes (
    id UUID PRIMARY KEY,
    deposit_id UUID NOT NULL UNIQUE REFERENCES deposits(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    circle_wallet_id VARCHAR(255) NOT NULL,
    yield_circle_wallet_id VARCHAR(255),
    yield_wallet_address VARCHAR(255),
    amount NUMERIC(36,18) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    circle_transfer_id VARCHAR(255),
    circle_tx_hash VARCHAR(255),
    chainrails_intent_id INTEGER,
    chainrails_intent_address VARCHAR(255),
    chainrails_source_chain VARCHAR(64),
    chainrails_destination_chain VARCHAR(64),
    chainrails_fund_amount NUMERIC(36,18),
    chainrails_fee_amount NUMERIC(36,18),
    chainrails_fee_debited_at TIMESTAMPTZ,
    chainrails_tx_hash VARCHAR(255),
    reflect_tx_hash VARCHAR(255),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reflect_deposit_routes_status_retry
    ON reflect_deposit_routes(status, next_retry_at);

CREATE INDEX IF NOT EXISTS idx_reflect_deposit_routes_user
    ON reflect_deposit_routes(user_id);

CREATE TABLE IF NOT EXISTS user_yield_positions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    route_id UUID NOT NULL UNIQUE REFERENCES reflect_deposit_routes(id) ON DELETE CASCADE,
    deposit_id UUID NOT NULL REFERENCES deposits(id) ON DELETE CASCADE,
    yield_circle_wallet_id VARCHAR(255),
    yield_wallet_address VARCHAR(255),
    principal_amount NUMERIC(36,18) NOT NULL,
    redeemed_amount NUMERIC(36,18) NOT NULL DEFAULT 0,
    receipt_tx_hash VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_yield_positions_user_status
    ON user_yield_positions(user_id, status);

CREATE TABLE IF NOT EXISTS user_yield_redemptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount NUMERIC(36,18) NOT NULL,
    tx_hash VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL UNIQUE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_yield_redemptions_user
    ON user_yield_redemptions(user_id);

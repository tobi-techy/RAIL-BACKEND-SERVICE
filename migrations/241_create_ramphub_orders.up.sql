-- RampHub on/off ramp orders.
-- RampHub is a best-rate aggregator (one API, routes across many providers).
-- This table tracks onramp (buy) and offramp (sell) orders for reconciliation,
-- mirroring the paj_orders pattern. No per-user session table is needed —
-- RampHub auth is a server-side x-api-key.
CREATE TABLE IF NOT EXISTS ramphub_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ramphub_transaction_id VARCHAR(255) NOT NULL UNIQUE,
    request_reference VARCHAR(255),
    order_type VARCHAR(10) NOT NULL,                 -- 'onramp' or 'offramp'
    status VARCHAR(20) NOT NULL DEFAULT 'pending',   -- pending, paid, processing, completed, failed
    selected_provider VARCHAR(100),                  -- underlying provider RampHub routed to
    -- Amounts
    fiat_amount DECIMAL(20, 2) NOT NULL,
    token_amount DECIMAL(20, 8),
    asset VARCHAR(10) NOT NULL DEFAULT 'USDC',       -- USDC / USDT
    chain VARCHAR(20) NOT NULL DEFAULT 'solana',
    currency VARCHAR(5) NOT NULL DEFAULT 'NGN',
    rate DECIMAL(20, 4),
    fee DECIMAL(20, 4),
    rail_fee_usdc DECIMAL(20, 8),                    -- Rail's USDC-denominated fee included in the hold
    hold_amount DECIMAL(20, 8),                      -- offramp: total USDC debited (transfer + rail fee + slippage)
    -- Onramp: bank account user pays into (from providerDetails)
    pay_account_number VARCHAR(40),
    pay_account_name VARCHAR(255),
    pay_bank VARCHAR(255),
    used_user_wallet BOOLEAN NOT NULL DEFAULT FALSE, -- onramp delivered straight to user's wallet
    -- Offramp: user's payout bank account
    bank_code VARCHAR(40),
    account_number VARCHAR(40),
    account_name VARCHAR(255),
    -- Offramp: RampHub's deposit address for the USDC we send
    our_crypto_address VARCHAR(255),
    -- Settlement / idempotency tracking
    bridge_transfer_id VARCHAR(255),                 -- "circle:<id>" or "circle-cr:<id>:<intent>"
    deposit_id UUID UNIQUE,                          -- idempotency claim slot (credit/reversal); UNIQUE prevents double-credit races
    withdrawal_id UUID,
    -- Webhook tracking
    last_webhook_status VARCHAR(40),
    last_webhook_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ramphub_orders_user ON ramphub_orders(user_id, created_at DESC);
CREATE INDEX idx_ramphub_orders_status ON ramphub_orders(status) WHERE status IN ('pending', 'paid', 'processing');
CREATE INDEX idx_ramphub_orders_txid ON ramphub_orders(ramphub_transaction_id);

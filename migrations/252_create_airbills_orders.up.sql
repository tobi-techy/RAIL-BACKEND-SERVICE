-- Airbills bill-payment orders (airtime, data, electricity, cable TV, betting,
-- transport). Airbills settles in USDC/USDT on Solana; Rail funds each payment
-- from the user's spend balance via a Circle USDC transfer, mirroring the
-- ramphub_orders off-ramp settlement pattern. This table tracks every payment
-- for reconciliation and recovery.
CREATE TABLE IF NOT EXISTS airbills_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    airbills_id VARCHAR(255) UNIQUE,                  -- Airbills transaction id (from /transact)
    request_reference VARCHAR(255),                   -- Rail-side idempotency reference
    product_code VARCHAR(8) NOT NULL,                 -- 100..105
    product_category VARCHAR(20) NOT NULL,            -- airtime, data, electricity, cable, betting, transport
    status VARCHAR(20) NOT NULL DEFAULT 'created',    -- created, held, sent, processing, completed, failed, reversed
    -- Payment target (PII: masked in logs, encrypted at rest where sensitive)
    beneficiary_id UUID,                              -- optional saved beneficiary used
    recipient VARCHAR(255),                           -- phone / meter / smartcard / betting id
    network_id VARCHAR(8),                            -- 01..04 for airtime/data
    prod_id VARCHAR(64),                              -- plan/provider id from lookup
    recipient_name VARCHAR(255),                      -- validated meter/account holder name
    -- Amounts
    amount_ngn DECIMAL(20, 2) NOT NULL,               -- face value in NGN
    token VARCHAR(10) NOT NULL DEFAULT 'USDC',        -- USDC / USDT
    amount_in_token DECIMAL(20, 8),                   -- amountInToken quoted by Airbills
    rail_fee_usdc DECIMAL(20, 8),                     -- Rail's service fee included in the hold
    hold_amount DECIMAL(20, 8),                       -- total USDC debited (token + rail fee)
    rate DECIMAL(20, 4),                              -- NGN per USD applied
    -- Airbills transfer-mode deposit address we send USDC to
    deposit_address VARCHAR(255),
    -- Settlement / idempotency tracking
    bridge_transfer_id VARCHAR(255),                  -- "circle:<id>" or "circle-cr:<id>:<intent>"
    deposit_id UUID UNIQUE,                           -- idempotency claim slot for hold/reversal
    withdrawal_id UUID,
    automation_id UUID,                               -- set when triggered by a recurring mandate
    -- Callback tracking
    last_callback_status VARCHAR(40),
    last_callback_at TIMESTAMP WITH TIME ZONE,
    failure_reason VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_airbills_orders_user ON airbills_orders(user_id, created_at DESC);
CREATE INDEX idx_airbills_orders_status ON airbills_orders(status) WHERE status IN ('created', 'held', 'sent', 'processing');
CREATE INDEX idx_airbills_orders_airbills_id ON airbills_orders(airbills_id);

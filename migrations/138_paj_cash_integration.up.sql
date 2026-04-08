-- Paj Cash NGN on/off ramp integration tables.

-- paj_sessions: cached per-user Paj session tokens.
-- Avoids re-authenticating with Paj OTP on every request.
CREATE TABLE IF NOT EXISTS paj_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE UNIQUE,
    session_token_encrypted TEXT NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- paj_orders: tracks onramp and offramp orders for reconciliation.
CREATE TABLE IF NOT EXISTS paj_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    paj_order_id VARCHAR(255) NOT NULL UNIQUE,
    order_type VARCHAR(10) NOT NULL, -- 'onramp' or 'offramp'
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, paid, completed, failed, expired
    fiat_amount DECIMAL(20, 2) NOT NULL,
    token_amount DECIMAL(20, 8),
    currency VARCHAR(5) NOT NULL DEFAULT 'NGN',
    rate DECIMAL(20, 4),
    fee DECIMAL(20, 4),
    -- Onramp: bank account user pays to
    pay_account_number VARCHAR(20),
    pay_account_name VARCHAR(255),
    pay_bank VARCHAR(255),
    -- Offramp: user's bank account receiving NGN
    bank_id VARCHAR(255),
    bank_account_number VARCHAR(20),
    -- Offramp: Paj deposit address for USDC
    paj_deposit_address VARCHAR(255),
    -- Linked deposit/withdrawal in Rail's system
    deposit_id UUID,
    withdrawal_id UUID,
    -- Webhook tracking
    last_webhook_status VARCHAR(20),
    last_webhook_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_paj_orders_user ON paj_orders(user_id, created_at DESC);
CREATE INDEX idx_paj_orders_status ON paj_orders(status) WHERE status IN ('pending', 'paid');
CREATE INDEX idx_paj_orders_paj_id ON paj_orders(paj_order_id);

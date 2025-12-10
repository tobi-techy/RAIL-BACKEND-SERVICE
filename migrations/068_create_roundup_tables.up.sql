-- Round-up settings per user
CREATE TABLE IF NOT EXISTS roundup_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT false,
    multiplier DECIMAL(3, 1) NOT NULL DEFAULT 1.0 CHECK (multiplier >= 1.0 AND multiplier <= 10.0),
    threshold DECIMAL(10, 2) NOT NULL DEFAULT 5.00 CHECK (threshold >= 1.00),
    auto_invest_enabled BOOLEAN NOT NULL DEFAULT false,
    auto_invest_basket_id UUID REFERENCES baskets(id),
    auto_invest_symbol VARCHAR(16),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT check_auto_invest_target CHECK (
        (auto_invest_enabled = false) OR 
        (auto_invest_basket_id IS NOT NULL OR auto_invest_symbol IS NOT NULL)
    )
);

-- Round-up transactions (linked card/bank transactions)
CREATE TABLE IF NOT EXISTS roundup_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    original_amount DECIMAL(20, 2) NOT NULL,
    rounded_amount DECIMAL(20, 2) NOT NULL,
    spare_change DECIMAL(20, 2) NOT NULL,
    multiplied_amount DECIMAL(20, 2) NOT NULL,
    source_type VARCHAR(20) NOT NULL, -- card, bank, manual
    source_ref VARCHAR(128),
    merchant_name VARCHAR(256),
    status VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending, collected, invested, failed
    collected_at TIMESTAMP WITH TIME ZONE,
    invested_at TIMESTAMP WITH TIME ZONE,
    investment_order_id UUID,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Round-up accumulator (pending balance before threshold)
CREATE TABLE IF NOT EXISTS roundup_accumulators (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    pending_amount DECIMAL(20, 2) NOT NULL DEFAULT 0,
    total_collected DECIMAL(20, 2) NOT NULL DEFAULT 0,
    total_invested DECIMAL(20, 2) NOT NULL DEFAULT 0,
    last_collection_at TIMESTAMP WITH TIME ZONE,
    last_investment_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_roundup_transactions_user ON roundup_transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_roundup_transactions_status ON roundup_transactions(status);
CREATE INDEX IF NOT EXISTS idx_roundup_transactions_created ON roundup_transactions(created_at);

-- Alpaca brokerage accounts linked to users
CREATE TABLE IF NOT EXISTS alpaca_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id VARCHAR(64) NOT NULL UNIQUE,
    alpaca_account_number VARCHAR(32),
    status VARCHAR(32) NOT NULL DEFAULT 'SUBMITTED',
    account_type VARCHAR(32) NOT NULL DEFAULT 'trading_cash',
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    buying_power DECIMAL(20, 8) DEFAULT 0,
    cash DECIMAL(20, 8) DEFAULT 0,
    portfolio_value DECIMAL(20, 8) DEFAULT 0,
    trading_blocked BOOLEAN DEFAULT FALSE,
    transfers_blocked BOOLEAN DEFAULT FALSE,
    account_blocked BOOLEAN DEFAULT FALSE,
    last_synced_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_alpaca_account UNIQUE (user_id)
);

-- Investment orders tracking
CREATE TABLE IF NOT EXISTS investment_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id UUID REFERENCES alpaca_accounts(id),
    alpaca_order_id VARCHAR(64) UNIQUE,
    client_order_id VARCHAR(64),
    basket_id UUID,
    symbol VARCHAR(16) NOT NULL,
    side VARCHAR(8) NOT NULL,
    order_type VARCHAR(16) NOT NULL DEFAULT 'market',
    time_in_force VARCHAR(8) NOT NULL DEFAULT 'day',
    qty DECIMAL(20, 9),
    notional DECIMAL(20, 2),
    filled_qty DECIMAL(20, 9) DEFAULT 0,
    filled_avg_price DECIMAL(20, 8),
    limit_price DECIMAL(20, 8),
    stop_price DECIMAL(20, 8),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    commission DECIMAL(20, 8) DEFAULT 0,
    submitted_at TIMESTAMP WITH TIME ZONE,
    filled_at TIMESTAMP WITH TIME ZONE,
    canceled_at TIMESTAMP WITH TIME ZONE,
    failed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Investment positions tracking
CREATE TABLE IF NOT EXISTS investment_positions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id UUID REFERENCES alpaca_accounts(id),
    symbol VARCHAR(16) NOT NULL,
    asset_id VARCHAR(64),
    qty DECIMAL(20, 9) NOT NULL DEFAULT 0,
    qty_available DECIMAL(20, 9) NOT NULL DEFAULT 0,
    avg_entry_price DECIMAL(20, 8) NOT NULL DEFAULT 0,
    market_value DECIMAL(20, 8) NOT NULL DEFAULT 0,
    cost_basis DECIMAL(20, 8) NOT NULL DEFAULT 0,
    unrealized_pl DECIMAL(20, 8) DEFAULT 0,
    unrealized_plpc DECIMAL(10, 6) DEFAULT 0,
    current_price DECIMAL(20, 8) DEFAULT 0,
    lastday_price DECIMAL(20, 8) DEFAULT 0,
    change_today DECIMAL(10, 6) DEFAULT 0,
    side VARCHAR(8) DEFAULT 'long',
    last_synced_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_symbol_position UNIQUE (user_id, symbol)
);

-- Alpaca instant funding transfers
CREATE TABLE IF NOT EXISTS alpaca_instant_funding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id UUID REFERENCES alpaca_accounts(id),
    alpaca_transfer_id VARCHAR(64) UNIQUE,
    source_account_no VARCHAR(32),
    amount DECIMAL(20, 8) NOT NULL,
    remaining_payable DECIMAL(20, 8),
    total_interest DECIMAL(20, 8) DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    deadline DATE,
    system_date DATE,
    settlement_id VARCHAR(64),
    settled_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Alpaca event log for audit trail
CREATE TABLE IF NOT EXISTS alpaca_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    alpaca_account_id UUID REFERENCES alpaca_accounts(id),
    event_type VARCHAR(64) NOT NULL,
    event_id VARCHAR(64),
    payload JSONB,
    processed BOOLEAN DEFAULT FALSE,
    processed_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_alpaca_accounts_user_id ON alpaca_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_alpaca_accounts_status ON alpaca_accounts(status);
CREATE INDEX IF NOT EXISTS idx_investment_orders_user_id ON investment_orders(user_id);
CREATE INDEX IF NOT EXISTS idx_investment_orders_status ON investment_orders(status);
CREATE INDEX IF NOT EXISTS idx_investment_orders_alpaca_order_id ON investment_orders(alpaca_order_id);
CREATE INDEX IF NOT EXISTS idx_investment_positions_user_id ON investment_positions(user_id);
CREATE INDEX IF NOT EXISTS idx_investment_positions_symbol ON investment_positions(symbol);
CREATE INDEX IF NOT EXISTS idx_alpaca_instant_funding_user_id ON alpaca_instant_funding(user_id);
CREATE INDEX IF NOT EXISTS idx_alpaca_instant_funding_status ON alpaca_instant_funding(status);
CREATE INDEX IF NOT EXISTS idx_alpaca_events_user_id ON alpaca_events(user_id);
CREATE INDEX IF NOT EXISTS idx_alpaca_events_event_type ON alpaca_events(event_type);
CREATE INDEX IF NOT EXISTS idx_alpaca_events_processed ON alpaca_events(processed) WHERE NOT processed;

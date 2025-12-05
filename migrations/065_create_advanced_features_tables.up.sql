-- Portfolio snapshots for performance tracking
CREATE TABLE IF NOT EXISTS portfolio_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_value DECIMAL(20, 8) NOT NULL,
    cash_value DECIMAL(20, 8) NOT NULL DEFAULT 0,
    invested_value DECIMAL(20, 8) NOT NULL DEFAULT 0,
    total_cost_basis DECIMAL(20, 8) NOT NULL DEFAULT 0,
    total_gain_loss DECIMAL(20, 8) NOT NULL DEFAULT 0,
    total_gain_loss_pct DECIMAL(10, 6) DEFAULT 0,
    day_gain_loss DECIMAL(20, 8) DEFAULT 0,
    day_gain_loss_pct DECIMAL(10, 6) DEFAULT 0,
    snapshot_date DATE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_user_snapshot_date UNIQUE (user_id, snapshot_date)
);

-- Scheduled investments (recurring buys / DCA)
CREATE TABLE IF NOT EXISTS scheduled_investments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(128),
    symbol VARCHAR(16),
    basket_id UUID,
    amount DECIMAL(20, 2) NOT NULL,
    frequency VARCHAR(16) NOT NULL, -- daily, weekly, biweekly, monthly
    day_of_week INTEGER, -- 0-6 for weekly
    day_of_month INTEGER, -- 1-28 for monthly
    next_execution_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_executed_at TIMESTAMP WITH TIME ZONE,
    status VARCHAR(16) NOT NULL DEFAULT 'active', -- active, paused, cancelled
    total_invested DECIMAL(20, 2) DEFAULT 0,
    execution_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT check_symbol_or_basket CHECK (symbol IS NOT NULL OR basket_id IS NOT NULL)
);

-- Scheduled investment executions log
CREATE TABLE IF NOT EXISTS scheduled_investment_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scheduled_investment_id UUID NOT NULL REFERENCES scheduled_investments(id) ON DELETE CASCADE,
    order_id UUID,
    amount DECIMAL(20, 2) NOT NULL,
    status VARCHAR(16) NOT NULL, -- success, failed, skipped
    error_message TEXT,
    executed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Rebalancing configurations
CREATE TABLE IF NOT EXISTS rebalancing_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    target_allocations JSONB NOT NULL, -- {"AAPL": 25, "GOOGL": 25, "VTI": 50}
    threshold_pct DECIMAL(5, 2) NOT NULL DEFAULT 5.0, -- rebalance when drift > threshold
    frequency VARCHAR(16), -- manual, daily, weekly, monthly, threshold_only
    last_rebalanced_at TIMESTAMP WITH TIME ZONE,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Market alerts
CREATE TABLE IF NOT EXISTS market_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    symbol VARCHAR(16) NOT NULL,
    alert_type VARCHAR(32) NOT NULL, -- price_above, price_below, pct_change, volume_spike
    condition_value DECIMAL(20, 8) NOT NULL,
    current_price DECIMAL(20, 8),
    triggered BOOLEAN DEFAULT FALSE,
    triggered_at TIMESTAMP WITH TIME ZONE,
    notification_sent BOOLEAN DEFAULT FALSE,
    status VARCHAR(16) NOT NULL DEFAULT 'active', -- active, triggered, cancelled
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_date ON portfolio_snapshots(user_id, snapshot_date DESC);
CREATE INDEX IF NOT EXISTS idx_scheduled_investments_user ON scheduled_investments(user_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_investments_next_exec ON scheduled_investments(next_execution_at) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_scheduled_executions_schedule ON scheduled_investment_executions(scheduled_investment_id);
CREATE INDEX IF NOT EXISTS idx_rebalancing_configs_user ON rebalancing_configs(user_id);
CREATE INDEX IF NOT EXISTS idx_market_alerts_user ON market_alerts(user_id);
CREATE INDEX IF NOT EXISTS idx_market_alerts_symbol ON market_alerts(symbol) WHERE status = 'active';

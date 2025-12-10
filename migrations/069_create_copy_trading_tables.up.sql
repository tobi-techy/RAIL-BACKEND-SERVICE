-- Conductors: Professional investors whose trades can be copied
CREATE TABLE IF NOT EXISTS conductors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    display_name VARCHAR(128) NOT NULL,
    bio TEXT,
    avatar_url VARCHAR(512),
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending, active, suspended
    fee_rate DECIMAL(5, 4) NOT NULL DEFAULT 0, -- Performance fee percentage (0.0000 - 1.0000)
    source_aum DECIMAL(20, 8) NOT NULL DEFAULT 0, -- Current USD value of conductor's portfolio
    total_return DECIMAL(10, 6) DEFAULT 0, -- Lifetime return percentage
    win_rate DECIMAL(5, 4) DEFAULT 0, -- Win rate (0-1)
    max_drawdown DECIMAL(10, 6) DEFAULT 0, -- Maximum drawdown percentage
    sharpe_ratio DECIMAL(10, 6) DEFAULT 0,
    total_trades INTEGER DEFAULT 0,
    followers_count INTEGER DEFAULT 0,
    min_draft_amount DECIMAL(20, 2) NOT NULL DEFAULT 100, -- Minimum capital to follow
    is_verified BOOLEAN DEFAULT FALSE,
    verified_at TIMESTAMP WITH TIME ZONE,
    last_trade_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_conductor_user UNIQUE (user_id)
);

-- Drafts: Active copy relationships between drafters and conductors
CREATE TABLE IF NOT EXISTS drafts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    drafter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conductor_id UUID NOT NULL REFERENCES conductors(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL DEFAULT 'active', -- active, paused, unlinking, unlinked
    allocated_capital DECIMAL(20, 8) NOT NULL, -- Total USD committed to this copy
    current_aum DECIMAL(20, 8) NOT NULL DEFAULT 0, -- Current USD value of draft portfolio
    start_value DECIMAL(20, 8) NOT NULL, -- Capital at moment of linking
    total_profit_loss DECIMAL(20, 8) DEFAULT 0,
    total_fees_paid DECIMAL(20, 8) DEFAULT 0,
    copy_ratio DECIMAL(5, 4) NOT NULL DEFAULT 1, -- 0-1, percentage of signals to copy
    auto_adjust BOOLEAN DEFAULT FALSE, -- Auto-adjust allocation based on performance
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    paused_at TIMESTAMP WITH TIME ZONE,
    unlinked_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT unique_drafter_conductor UNIQUE (drafter_id, conductor_id),
    CONSTRAINT check_allocated_capital_positive CHECK (allocated_capital > 0)
);

-- Signals: Trades executed by conductors
CREATE TABLE IF NOT EXISTS signals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conductor_id UUID NOT NULL REFERENCES conductors(id) ON DELETE CASCADE,
    asset_ticker VARCHAR(16) NOT NULL,
    asset_name VARCHAR(128),
    signal_type VARCHAR(16) NOT NULL, -- BUY, SELL, REBALANCE
    side VARCHAR(8) NOT NULL, -- buy, sell
    base_quantity DECIMAL(20, 8) NOT NULL, -- Conductor's original trade quantity
    base_price DECIMAL(20, 8) NOT NULL, -- Price at execution
    base_value DECIMAL(20, 8) NOT NULL, -- Total value of trade
    conductor_aum_at_signal DECIMAL(20, 8) NOT NULL, -- Conductor's AUM at time of signal
    order_id VARCHAR(128), -- Reference to original order
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending, processing, completed, failed
    processed_count INTEGER DEFAULT 0, -- Number of drafts processed
    failed_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Signal Execution Logs: Track copied trade execution for each drafter
CREATE TABLE IF NOT EXISTS signal_execution_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id UUID NOT NULL REFERENCES drafts(id) ON DELETE CASCADE,
    signal_id UUID NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
    executed_quantity DECIMAL(20, 8) NOT NULL,
    executed_price DECIMAL(20, 8) NOT NULL,
    executed_value DECIMAL(20, 8) NOT NULL,
    status VARCHAR(32) NOT NULL, -- success, partial, skipped_too_small, insufficient_funds, failed
    fee_applied DECIMAL(20, 8) DEFAULT 0,
    error_message TEXT,
    order_id VARCHAR(128), -- Brokerage order reference
    idempotency_key VARCHAR(128) NOT NULL, -- For preventing duplicate executions
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    executed_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT unique_draft_signal UNIQUE (draft_id, signal_id),
    CONSTRAINT unique_idempotency_key UNIQUE (idempotency_key)
);

-- Conductor performance history (daily snapshots)
CREATE TABLE IF NOT EXISTS conductor_performance_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conductor_id UUID NOT NULL REFERENCES conductors(id) ON DELETE CASCADE,
    snapshot_date DATE NOT NULL,
    aum DECIMAL(20, 8) NOT NULL,
    daily_return DECIMAL(10, 6) DEFAULT 0,
    cumulative_return DECIMAL(10, 6) DEFAULT 0,
    followers_count INTEGER DEFAULT 0,
    trades_count INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_conductor_date UNIQUE (conductor_id, snapshot_date)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_conductors_status ON conductors(status) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_conductors_followers ON conductors(followers_count DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_conductors_return ON conductors(total_return DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_drafts_drafter ON drafts(drafter_id);
CREATE INDEX IF NOT EXISTS idx_drafts_conductor ON drafts(conductor_id);
CREATE INDEX IF NOT EXISTS idx_drafts_status ON drafts(status) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_signals_conductor ON signals(conductor_id);
CREATE INDEX IF NOT EXISTS idx_signals_status ON signals(status) WHERE status IN ('pending', 'processing');
CREATE INDEX IF NOT EXISTS idx_signals_created ON signals(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_execution_logs_draft ON signal_execution_logs(draft_id);
CREATE INDEX IF NOT EXISTS idx_execution_logs_signal ON signal_execution_logs(signal_id);
CREATE INDEX IF NOT EXISTS idx_performance_history_conductor ON conductor_performance_history(conductor_id, snapshot_date DESC);

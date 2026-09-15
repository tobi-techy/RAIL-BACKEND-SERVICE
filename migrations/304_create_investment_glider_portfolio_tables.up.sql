-- Investment (Glider) portfolio tables
-- One enrollment per user strategy: the user's portfolio on the provider.
-- Holdings are the normalized read model of provider/chain state, and
-- executions are the auditable record of every portfolio action.

CREATE TABLE IF NOT EXISTS investment_enrollments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    strategy_id UUID NOT NULL REFERENCES investment_strategies(id) ON DELETE RESTRICT,
    strategy_version INTEGER NOT NULL DEFAULT 0,
    glider_portfolio_id TEXT NOT NULL DEFAULT '',
    glider_strategy_id TEXT NOT NULL DEFAULT '',
    chain VARCHAR(64) NOT NULL DEFAULT 'solana',
    owner_account_id TEXT NOT NULL DEFAULT '',
    agent_account_id TEXT NOT NULL DEFAULT '',
    deposit_account_id TEXT NOT NULL DEFAULT '',
    swig_role_id INTEGER,
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING_SIGNATURE',
    automation_status VARCHAR(24) NOT NULL DEFAULT '',
    next_due_at TIMESTAMP WITH TIME ZONE,
    last_rebalance_at TIMESTAMP WITH TIME ZONE,
    total_value_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    positions_as_of TIMESTAMP WITH TIME ZONE,
    last_sync_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_investment_enrollments_user ON investment_enrollments (user_id, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_enrollments_user_strategy ON investment_enrollments (user_id, strategy_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_enrollments_portfolio ON investment_enrollments (glider_portfolio_id) WHERE glider_portfolio_id <> '';
CREATE INDEX IF NOT EXISTS idx_investment_enrollments_active ON investment_enrollments (status) WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS investment_holdings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID NOT NULL REFERENCES investment_enrollments(id) ON DELETE CASCADE,
    asset_id UUID REFERENCES investment_assets(id) ON DELETE SET NULL,
    caip19 TEXT NOT NULL,
    symbol VARCHAR(32) NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    balance NUMERIC(38, 18) NOT NULL DEFAULT 0,
    balance_raw TEXT NOT NULL DEFAULT '',
    decimals INTEGER NOT NULL DEFAULT 6,
    price_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    value_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    weight_pct NUMERIC(9, 4) NOT NULL DEFAULT 0,
    source VARCHAR(32) NOT NULL DEFAULT 'glider',
    as_of TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (enrollment_id, caip19)
);

CREATE INDEX IF NOT EXISTS idx_investment_holdings_user ON investment_holdings (user_id);
CREATE INDEX IF NOT EXISTS idx_investment_holdings_enrollment ON investment_holdings (enrollment_id);

CREATE TABLE IF NOT EXISTS investment_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID REFERENCES investment_enrollments(id) ON DELETE SET NULL,
    strategy_id UUID REFERENCES investment_strategies(id) ON DELETE SET NULL,
    strategy_version INTEGER,
    kind VARCHAR(24) NOT NULL,
    side VARCHAR(8) NOT NULL DEFAULT '',
    asset_id UUID REFERENCES investment_assets(id) ON DELETE SET NULL,
    symbol VARCHAR(32) NOT NULL DEFAULT '',
    requested_amount_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    validated_amount_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    status VARCHAR(24) NOT NULL DEFAULT 'REQUESTED',
    idempotency_key TEXT,
    policy JSONB,
    provider VARCHAR(24) NOT NULL DEFAULT 'glider',
    provider_operation_id TEXT NOT NULL DEFAULT '',
    provider_tx_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    failure_code VARCHAR(64) NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    market_data_as_of TIMESTAMP WITH TIME ZONE,
    requested_by VARCHAR(16) NOT NULL DEFAULT 'system',
    confirmation_method VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_investment_executions_user ON investment_executions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_investment_executions_enrollment ON investment_executions (enrollment_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_executions_idempotency ON investment_executions (idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Investment (Glider) strategy tables
-- Assets the engine may allocate to, strategy identities, and immutable
-- strategy versions. A strategy's allocation only ever changes by publishing
-- a new version, so an active strategy is never silently mutated.

CREATE TABLE IF NOT EXISTS investment_assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    caip19 TEXT NOT NULL UNIQUE,
    symbol VARCHAR(32) NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    asset_class VARCHAR(32) NOT NULL DEFAULT 'crypto',
    chain VARCHAR(64) NOT NULL DEFAULT 'solana',
    decimals INTEGER NOT NULL DEFAULT 6,
    allowlisted BOOLEAN NOT NULL DEFAULT false,
    prohibited BOOLEAN NOT NULL DEFAULT false,
    source VARCHAR(32) NOT NULL DEFAULT 'glider',
    priority INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investment_assets_symbol ON investment_assets (symbol);
CREATE INDEX IF NOT EXISTS idx_investment_assets_allowlisted ON investment_assets (allowlisted) WHERE allowlisted = true;
CREATE INDEX IF NOT EXISTS idx_investment_assets_class ON investment_assets (asset_class);

CREATE TABLE IF NOT EXISTS investment_strategies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    owner_type VARCHAR(24) NOT NULL DEFAULT 'user',
    glider_strategy_id TEXT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    objective TEXT NOT NULL DEFAULT '',
    risk VARCHAR(16) NOT NULL DEFAULT '',
    horizon VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'DRAFT',
    current_version INTEGER NOT NULL DEFAULT 0,
    is_public BOOLEAN NOT NULL DEFAULT false,
    created_by VARCHAR(16) NOT NULL DEFAULT 'system',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_investment_strategies_user ON investment_strategies (user_id, status);
CREATE INDEX IF NOT EXISTS idx_investment_strategies_status ON investment_strategies (status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_strategies_glider ON investment_strategies (glider_strategy_id) WHERE glider_strategy_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS investment_strategy_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID NOT NULL REFERENCES investment_strategies(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    target_allocation JSONB NOT NULL,
    risk VARCHAR(16) NOT NULL DEFAULT '',
    horizon VARCHAR(32) NOT NULL DEFAULT '',
    rebalance_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    contribution_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    execution_rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    constraints JSONB NOT NULL DEFAULT '{}'::jsonb,
    rationale TEXT NOT NULL DEFAULT '',
    validation_report JSONB,
    glider_strategy_version INTEGER,
    created_by VARCHAR(16) NOT NULL DEFAULT 'system',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (strategy_id, version)
);

CREATE INDEX IF NOT EXISTS idx_investment_strategy_versions_strategy ON investment_strategy_versions (strategy_id, version DESC);

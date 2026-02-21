-- Investment Rules Tables Migration
-- Creates tables for investment rules, milestones, pending withdrawals, and dividends

-- Investment Rules Config table
CREATE TABLE IF NOT EXISTS investment_rules_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    age_based_allocation JSONB,
    rebalancing_config JSONB,
    drip_config JSONB,
    withdrawal_cooling JSONB,
    round_up_multiplier INTEGER NOT NULL DEFAULT 1,
    milestone_notifications BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_investment_rules_user_id ON investment_rules_config(user_id);
CREATE INDEX idx_investment_rules_rebalancing ON investment_rules_config((rebalancing_config->>'enabled')) WHERE rebalancing_config->>'enabled' = 'true';

-- Investment Milestones table
CREATE TABLE IF NOT EXISTS investment_milestones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    achieved_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    celebrated BOOLEAN NOT NULL DEFAULT false,
    celebrated_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(user_id, type, amount)
);

CREATE INDEX idx_milestones_user_id ON investment_milestones(user_id);
CREATE INDEX idx_milestones_uncelebrated ON investment_milestones(user_id, celebrated) WHERE celebrated = false;

-- Pending Withdrawals table (for cooling-off period)
CREATE TABLE IF NOT EXISTS pending_withdrawals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(20, 8) NOT NULL,
    requested_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    execute_after TIMESTAMP WITH TIME ZONE NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    cancelled_at TIMESTAMP WITH TIME ZONE,
    executed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_pending_withdrawals_user_id ON pending_withdrawals(user_id);
CREATE INDEX idx_pending_withdrawals_status ON pending_withdrawals(status) WHERE status = 'pending';
CREATE INDEX idx_pending_withdrawals_ready ON pending_withdrawals(execute_after) WHERE status = 'pending';

-- Dividend Events table (for DRIP)
CREATE TABLE IF NOT EXISTS dividend_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    symbol VARCHAR(20) NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    shares_held DECIMAL(20, 8) NOT NULL,
    ex_date DATE,
    pay_date DATE,
    received_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    reinvested BOOLEAN NOT NULL DEFAULT false,
    reinvested_at TIMESTAMP WITH TIME ZONE,
    reinvest_order_id UUID
);

CREATE INDEX idx_dividend_events_user_id ON dividend_events(user_id);
CREATE INDEX idx_dividend_events_pending ON dividend_events(reinvested) WHERE reinvested = false;
CREATE INDEX idx_dividend_events_symbol ON dividend_events(user_id, symbol);

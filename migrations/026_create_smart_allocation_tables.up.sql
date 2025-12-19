-- Smart Allocation Mode Tables Migration
-- This migration creates all required tables for the 70/30 Smart Allocation Mode feature

-- Smart allocation mode state per user
CREATE TABLE IF NOT EXISTS smart_allocation_mode (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    ratio_spending NUMERIC(5,4) NOT NULL DEFAULT 0.70 CHECK (ratio_spending >= 0 AND ratio_spending <= 1),
    ratio_stash NUMERIC(5,4) NOT NULL DEFAULT 0.30 CHECK (ratio_stash >= 0 AND ratio_stash <= 1),
    paused_at TIMESTAMP WITH TIME ZONE,
    resumed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Ensure ratios sum to 1.0
    CONSTRAINT valid_ratio_sum CHECK (ratio_spending + ratio_stash = 1.0)
);

CREATE INDEX IF NOT EXISTS idx_smart_allocation_mode_active ON smart_allocation_mode(active);
CREATE INDEX IF NOT EXISTS idx_smart_allocation_mode_updated_at ON smart_allocation_mode(updated_at);

-- Allocation events for audit trail
CREATE TABLE IF NOT EXISTS allocation_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_amount NUMERIC(36,18) NOT NULL CHECK (total_amount > 0),
    stash_amount NUMERIC(36,18) NOT NULL CHECK (stash_amount >= 0),
    spending_amount NUMERIC(36,18) NOT NULL CHECK (spending_amount >= 0),
    event_type VARCHAR(20) NOT NULL CHECK (event_type IN ('deposit', 'fiat_deposit', 'crypto_deposit', 'cashback', 'roundup', 'transfer')),
    source_tx_id TEXT,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_allocation_events_user_id ON allocation_events(user_id);
CREATE INDEX IF NOT EXISTS idx_allocation_events_event_type ON allocation_events(event_type);
CREATE INDEX IF NOT EXISTS idx_allocation_events_created_at ON allocation_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_allocation_events_user_created ON allocation_events(user_id, created_at DESC);

-- Weekly allocation summaries for analytics
CREATE TABLE IF NOT EXISTS weekly_allocation_summaries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    week_start DATE NOT NULL,
    week_end DATE NOT NULL,
    total_income NUMERIC(36,18) NOT NULL DEFAULT 0,
    stash_added NUMERIC(36,18) NOT NULL DEFAULT 0,
    spending_added NUMERIC(36,18) NOT NULL DEFAULT 0,
    spending_used NUMERIC(36,18) NOT NULL DEFAULT 0,
    spending_remaining NUMERIC(36,18) NOT NULL DEFAULT 0,
    declines_count INT NOT NULL DEFAULT 0,
    mode_active_days INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Unique constraint to prevent duplicate weekly summaries
    CONSTRAINT unique_user_week UNIQUE (user_id, week_start)
);

CREATE INDEX IF NOT EXISTS idx_weekly_allocation_summaries_user_id ON weekly_allocation_summaries(user_id);
CREATE INDEX IF NOT EXISTS idx_weekly_allocation_summaries_week_start ON weekly_allocation_summaries(week_start);
CREATE INDEX IF NOT EXISTS idx_weekly_allocation_summaries_created_at ON weekly_allocation_summaries(created_at);

-- Extend transactions table with decline tracking
ALTER TABLE transactions
ADD COLUMN IF NOT EXISTS declined_due_to_7030 BOOLEAN DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_transactions_declined ON transactions(declined_due_to_7030) WHERE declined_due_to_7030 = TRUE;

-- Add trigger to update smart_allocation_mode.updated_at
CREATE OR REPLACE FUNCTION update_smart_allocation_mode_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS update_smart_allocation_mode_updated_at_trigger ON smart_allocation_mode;
CREATE TRIGGER update_smart_allocation_mode_updated_at_trigger
    BEFORE UPDATE ON smart_allocation_mode
    FOR EACH ROW
    EXECUTE FUNCTION update_smart_allocation_mode_updated_at();

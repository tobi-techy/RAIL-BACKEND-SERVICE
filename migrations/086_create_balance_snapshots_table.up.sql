-- Migration: Create Balance Snapshots Table
-- Purpose: Store daily balance snapshots for trend calculation (day/week/month changes)

CREATE TABLE IF NOT EXISTS balance_snapshots (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    snapshot_date DATE NOT NULL,
    spend_balance DECIMAL(36, 18) NOT NULL DEFAULT 0,
    invest_balance DECIMAL(36, 18) NOT NULL DEFAULT 0,
    total_balance DECIMAL(36, 18) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    CONSTRAINT uq_balance_snapshots_user_date UNIQUE(user_id, snapshot_date)
);

CREATE INDEX idx_balance_snapshots_user_id ON balance_snapshots(user_id);
CREATE INDEX idx_balance_snapshots_date ON balance_snapshots(snapshot_date DESC);
CREATE INDEX idx_balance_snapshots_user_date ON balance_snapshots(user_id, snapshot_date DESC);

COMMENT ON TABLE balance_snapshots IS 'Daily balance snapshots for calculating balance trends';
COMMENT ON COLUMN balance_snapshots.snapshot_date IS 'Date of the snapshot (one per user per day)';

-- Create instant_fundings table for simplified instant funding
CREATE TABLE IF NOT EXISTS instant_fundings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id VARCHAR(255) NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    journal_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMP WITH TIME ZONE,
    
    CONSTRAINT instant_fundings_status_check CHECK (status IN ('active', 'settled', 'repaid'))
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_instant_fundings_user_id ON instant_fundings(user_id);
CREATE INDEX IF NOT EXISTS idx_instant_fundings_user_status ON instant_fundings(user_id, status);
CREATE INDEX IF NOT EXISTS idx_instant_fundings_created_at ON instant_fundings(created_at DESC);

-- Add comments
COMMENT ON TABLE instant_fundings IS 'Simplified instant funding records for trading';
COMMENT ON COLUMN instant_fundings.status IS 'active: buying power granted, settled: wire completed, repaid: early repayment';

-- Add off-ramp tracking fields to deposits table
ALTER TABLE deposits
ADD COLUMN IF NOT EXISTS off_ramp_tx_id VARCHAR(100),
ADD COLUMN IF NOT EXISTS off_ramp_initiated_at TIMESTAMP WITH TIME ZONE,
ADD COLUMN IF NOT EXISTS off_ramp_completed_at TIMESTAMP WITH TIME ZONE,
ADD COLUMN IF NOT EXISTS alpaca_funding_tx_id VARCHAR(100),
ADD COLUMN IF NOT EXISTS alpaca_funded_at TIMESTAMP WITH TIME ZONE,
ADD COLUMN IF NOT EXISTS virtual_account_id UUID REFERENCES virtual_accounts(id);

-- Create indexes for new fields
CREATE INDEX IF NOT EXISTS idx_deposits_off_ramp_tx_id ON deposits(off_ramp_tx_id);
CREATE INDEX IF NOT EXISTS idx_deposits_alpaca_funding_tx_id ON deposits(alpaca_funding_tx_id);
CREATE INDEX IF NOT EXISTS idx_deposits_virtual_account_id ON deposits(virtual_account_id);

-- Update status check constraint to include new statuses
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check 
CHECK (status IN ('pending', 'confirmed', 'failed', 'off_ramp_initiated', 'off_ramp_completed', 'broker_funded'));

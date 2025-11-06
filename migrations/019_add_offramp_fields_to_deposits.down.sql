-- Remove off-ramp tracking fields from deposits table
ALTER TABLE deposits
DROP COLUMN IF EXISTS virtual_account_id,
DROP COLUMN IF EXISTS alpaca_funded_at,
DROP COLUMN IF EXISTS alpaca_funding_tx_id,
DROP COLUMN IF EXISTS off_ramp_completed_at,
DROP COLUMN IF EXISTS off_ramp_initiated_at,
DROP COLUMN IF EXISTS off_ramp_tx_id;

-- Restore original status check constraint
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check 
CHECK (status IN ('pending', 'confirmed', 'failed'));

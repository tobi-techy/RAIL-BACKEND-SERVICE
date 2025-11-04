-- Remove off-ramp tracking fields from deposits table
-- Reverts Due API off-ramp integration

-- Drop indexes first
DROP INDEX IF EXISTS idx_deposits_due_transfer_reference;
DROP INDEX IF EXISTS idx_deposits_off_ramp_completed_at;
DROP INDEX IF EXISTS idx_deposits_off_ramp_initiated_at;

-- Remove columns
ALTER TABLE deposits
DROP COLUMN IF EXISTS due_transfer_reference,
DROP COLUMN IF EXISTS off_ramp_completed_at,
DROP COLUMN IF EXISTS off_ramp_initiated_at;

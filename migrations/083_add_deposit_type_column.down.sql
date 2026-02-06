-- Revert deposit_type column
DROP INDEX IF EXISTS idx_deposits_deposit_type;
ALTER TABLE deposits DROP COLUMN IF EXISTS deposit_type;

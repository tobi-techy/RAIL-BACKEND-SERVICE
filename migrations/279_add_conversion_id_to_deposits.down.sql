DROP INDEX IF EXISTS idx_deposits_conversion_id;
ALTER TABLE deposits DROP COLUMN IF EXISTS conversion_id;

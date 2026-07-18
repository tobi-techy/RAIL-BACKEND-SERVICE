DROP INDEX IF EXISTS idx_signals_conductor_order;
DROP INDEX IF EXISTS idx_conductors_external_key;
-- Remove public-figure conductors before restoring the NOT NULL constraint.
DELETE FROM conductors WHERE user_id IS NULL;
ALTER TABLE conductors DROP COLUMN IF EXISTS external_key;
ALTER TABLE conductors DROP COLUMN IF EXISTS source;
ALTER TABLE conductors ALTER COLUMN user_id SET NOT NULL;

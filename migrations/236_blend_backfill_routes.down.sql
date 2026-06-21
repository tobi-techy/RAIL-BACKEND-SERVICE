-- ⚠️  DESTRUCTIVE ROLLBACK — permanently removes all backfilled Blend data.
-- Stop any running backfill operations before executing.
-- Requires explicit approval; review carefully before running in production.

BEGIN;

-- Archive backfill rows before deletion
CREATE TABLE IF NOT EXISTS blend_deposit_routes_archive (LIKE blend_deposit_routes INCLUDING ALL);
CREATE TABLE IF NOT EXISTS blend_yield_positions_archive (LIKE blend_yield_positions INCLUDING ALL);

INSERT INTO blend_deposit_routes_archive SELECT * FROM blend_deposit_routes WHERE deposit_id IS NULL ON CONFLICT DO NOTHING;
INSERT INTO blend_yield_positions_archive SELECT * FROM blend_yield_positions WHERE deposit_id IS NULL ON CONFLICT DO NOTHING;

DROP INDEX IF EXISTS idx_blend_deposit_routes_source;

DELETE FROM blend_deposit_routes WHERE deposit_id IS NULL;
DELETE FROM blend_yield_positions WHERE deposit_id IS NULL;

ALTER TABLE blend_deposit_routes DROP COLUMN IF EXISTS source;
ALTER TABLE blend_deposit_routes ALTER COLUMN deposit_id SET NOT NULL;
ALTER TABLE blend_yield_positions ALTER COLUMN deposit_id SET NOT NULL;

COMMIT;

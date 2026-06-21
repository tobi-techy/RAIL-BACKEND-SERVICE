DROP INDEX IF EXISTS idx_blend_deposit_routes_source;

-- Best-effort revert. Backfill rows (NULL deposit_id) must be cleared before re-adding NOT NULL.
DELETE FROM blend_deposit_routes WHERE deposit_id IS NULL;
DELETE FROM blend_yield_positions WHERE deposit_id IS NULL;

ALTER TABLE blend_deposit_routes DROP COLUMN IF EXISTS source;
ALTER TABLE blend_deposit_routes ALTER COLUMN deposit_id SET NOT NULL;
ALTER TABLE blend_yield_positions ALTER COLUMN deposit_id SET NOT NULL;

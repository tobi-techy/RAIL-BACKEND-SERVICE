-- ⚠️  DESTRUCTIVE ROLLBACK — permanently removes all backfilled Blend data.
-- Stop any running backfill operations before executing.
-- Requires explicit approval; review carefully before running in production.

-- Fail fast if any active queries are touching blend tables concurrently.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_stat_activity
    WHERE pid <> pg_backend_pid()
      AND state = 'active'
      AND (
        (query ~* '(INSERT|UPDATE|DELETE).*blend_deposit_routes')
        OR (query ~* '(INSERT|UPDATE|DELETE).*blend_yield_positions')
      )
  ) THEN
    RAISE EXCEPTION 'Active write operations detected on blend tables. Stop backfill operations before running this rollback.';
  END IF;
END $$;

BEGIN;

-- Archive backfill rows before deletion (fresh tables per rollback attempt)
DROP TABLE IF EXISTS blend_deposit_routes_archive;
DROP TABLE IF EXISTS blend_yield_positions_archive;

CREATE TABLE blend_deposit_routes_archive (LIKE blend_deposit_routes INCLUDING ALL);
CREATE TABLE blend_yield_positions_archive (LIKE blend_yield_positions INCLUDING ALL);

INSERT INTO blend_deposit_routes_archive SELECT * FROM blend_deposit_routes WHERE deposit_id IS NULL;
INSERT INTO blend_yield_positions_archive SELECT * FROM blend_yield_positions WHERE deposit_id IS NULL;

DROP INDEX IF EXISTS idx_blend_deposit_routes_source;

DELETE FROM blend_deposit_routes WHERE deposit_id IS NULL;
DELETE FROM blend_yield_positions WHERE deposit_id IS NULL;

ALTER TABLE blend_deposit_routes DROP COLUMN IF EXISTS source;
ALTER TABLE blend_deposit_routes ALTER COLUMN deposit_id SET NOT NULL;
ALTER TABLE blend_yield_positions ALTER COLUMN deposit_id SET NOT NULL;

COMMIT;

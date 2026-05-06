DROP INDEX IF EXISTS idx_stash_transfers_direction;
ALTER TABLE stash_transfers DROP CONSTRAINT IF EXISTS chk_stash_transfers_direction;
ALTER TABLE stash_transfers DROP COLUMN IF EXISTS direction;

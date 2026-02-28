-- Remove rail_tag column from users table
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_rail_tag_format;
DROP INDEX IF EXISTS idx_users_rail_tag;
ALTER TABLE users DROP COLUMN IF EXISTS rail_tag;

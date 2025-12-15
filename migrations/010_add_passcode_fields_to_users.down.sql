DROP INDEX IF EXISTS idx_users_passcode_locked_until;

ALTER TABLE users
    DROP COLUMN IF EXISTS passcode_hash,
    DROP COLUMN IF EXISTS passcode_failed_attempts,
    DROP COLUMN IF EXISTS passcode_locked_until,
    DROP COLUMN IF EXISTS passcode_updated_at;

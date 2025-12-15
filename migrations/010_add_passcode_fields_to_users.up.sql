ALTER TABLE users
    ADD COLUMN IF NOT EXISTS passcode_hash TEXT,
    ADD COLUMN IF NOT EXISTS passcode_failed_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS passcode_locked_until TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS passcode_updated_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_users_passcode_locked_until ON users(passcode_locked_until) WHERE passcode_locked_until IS NOT NULL;

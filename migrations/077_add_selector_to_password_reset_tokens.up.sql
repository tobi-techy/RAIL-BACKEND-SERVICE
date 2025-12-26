-- Add selector column for selector-verifier pattern
-- selector: public, indexed for fast lookup
-- token_hash: now stores hash of verifier only (not full token)

-- Delete existing tokens as they use the old scheme and cannot be migrated
-- Password reset tokens are short-lived, so this is acceptable
DELETE FROM password_reset_tokens;

-- Drop the old token_hash index (no longer needed for lookups)
-- Note: CONCURRENTLY cannot be used inside a transaction block
DROP INDEX IF EXISTS idx_password_reset_tokens_token_hash;

-- Add selector column with NOT NULL constraint (safe since table is empty)
ALTER TABLE password_reset_tokens ADD COLUMN selector VARCHAR(32) NOT NULL;

-- Create index on selector for fast lookups
CREATE INDEX idx_password_reset_tokens_selector ON password_reset_tokens(selector);

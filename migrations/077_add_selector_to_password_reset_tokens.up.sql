-- Add selector column for selector-verifier pattern
-- selector: public, indexed for fast lookup
-- token_hash: now stores hash of verifier only (not full token)

ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS selector VARCHAR(32);

-- Create index on selector for fast lookups
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_selector ON password_reset_tokens(selector);

-- Drop the old token_hash index as we'll use selector for lookups
DROP INDEX IF EXISTS idx_password_reset_tokens_token_hash;

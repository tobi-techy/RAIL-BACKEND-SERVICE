-- Revert selector-verifier pattern changes
-- WARNING: After rollback, token_hash contains verifier hashes (not original full-token hashes).
-- Existing tokens will be invalid and users must request new password reset links.
-- This is acceptable as password reset tokens are short-lived (typically 1 hour).

-- Drop the selector index
DROP INDEX IF EXISTS idx_password_reset_tokens_selector;

-- Remove the selector column
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS selector;

-- Recreate the original token_hash index for the old lookup pattern
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_token_hash ON password_reset_tokens(token_hash);

-- Note: Any tokens created with the new scheme will have verifier hashes in token_hash
-- and cannot be validated with the old code. Users must request new reset links.

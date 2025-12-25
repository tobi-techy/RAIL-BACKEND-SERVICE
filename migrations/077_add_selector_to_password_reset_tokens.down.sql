-- Revert selector-verifier pattern changes
DROP INDEX IF EXISTS idx_password_reset_tokens_selector;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS selector;
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_token_hash ON password_reset_tokens(token_hash);

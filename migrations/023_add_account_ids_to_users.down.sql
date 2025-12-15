-- Remove Due and Alpaca account IDs from users table
DROP INDEX IF EXISTS idx_users_due_account_id;
DROP INDEX IF EXISTS idx_users_alpaca_account_id;

ALTER TABLE users 
DROP COLUMN IF EXISTS due_account_id,
DROP COLUMN IF EXISTS alpaca_account_id;
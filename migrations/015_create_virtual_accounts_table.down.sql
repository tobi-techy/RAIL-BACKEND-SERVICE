-- Drop virtual_accounts table
DROP TRIGGER IF EXISTS update_virtual_accounts_updated_at ON virtual_accounts;
DROP INDEX IF EXISTS idx_virtual_accounts_status;
DROP INDEX IF EXISTS idx_virtual_accounts_alpaca_account_id;
DROP INDEX IF EXISTS idx_virtual_accounts_due_account_id;
DROP INDEX IF EXISTS idx_virtual_accounts_user_id;
DROP TABLE IF EXISTS virtual_accounts;
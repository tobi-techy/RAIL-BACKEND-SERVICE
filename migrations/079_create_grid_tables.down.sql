-- Drop indexes
DROP INDEX IF EXISTS idx_grid_payment_intents_status;
DROP INDEX IF EXISTS idx_grid_payment_intents_grid_account_id;
DROP INDEX IF EXISTS idx_grid_payment_intents_user_id;
DROP INDEX IF EXISTS idx_grid_virtual_accounts_grid_account_id;
DROP INDEX IF EXISTS idx_grid_virtual_accounts_user_id;
DROP INDEX IF EXISTS idx_grid_accounts_address;
DROP INDEX IF EXISTS idx_grid_accounts_email;
DROP INDEX IF EXISTS idx_grid_accounts_user_id;

-- Drop tables
DROP TABLE IF EXISTS grid_payment_intents;
DROP TABLE IF EXISTS grid_virtual_accounts;
DROP TABLE IF EXISTS grid_accounts;

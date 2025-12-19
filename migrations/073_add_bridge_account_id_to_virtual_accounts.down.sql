-- Remove bridge_account_id column from virtual_accounts table
DROP INDEX IF EXISTS idx_virtual_accounts_bridge_account_id;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS bridge_account_id;

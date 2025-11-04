-- Remove virtual accounts table and related objects

-- Drop RLS policies
DROP POLICY IF EXISTS virtual_accounts_own ON virtual_accounts;
DROP POLICY IF EXISTS virtual_accounts_admin ON virtual_accounts;

-- Disable RLS
ALTER TABLE virtual_accounts DISABLE ROW LEVEL SECURITY;

-- Drop trigger
DROP TRIGGER IF EXISTS trigger_virtual_accounts_updated_at ON virtual_accounts;

-- Drop function
DROP FUNCTION IF EXISTS update_virtual_accounts_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_virtual_accounts_user_id;
DROP INDEX IF EXISTS idx_virtual_accounts_status;
DROP INDEX IF EXISTS idx_virtual_accounts_due_account_id;
DROP INDEX IF EXISTS idx_virtual_accounts_brokerage_account_id;

-- Drop constraint
ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS unique_brokerage_account_id;

-- Drop table
DROP TABLE IF EXISTS virtual_accounts;

-- Drop enum
DROP TYPE IF EXISTS virtual_account_status;

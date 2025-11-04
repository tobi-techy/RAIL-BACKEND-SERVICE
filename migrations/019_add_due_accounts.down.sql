-- Remove due_accounts table and related objects

-- Drop RLS policies
DROP POLICY IF EXISTS due_accounts_admin ON due_accounts;
DROP POLICY IF EXISTS due_accounts_own ON due_accounts;

-- Drop trigger and function
DROP TRIGGER IF EXISTS trigger_due_accounts_updated_at ON due_accounts;
DROP FUNCTION IF EXISTS update_due_accounts_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_due_accounts_type;
DROP INDEX IF EXISTS idx_due_accounts_status;
DROP INDEX IF EXISTS idx_due_accounts_due_id;
DROP INDEX IF EXISTS idx_due_accounts_user_id;

-- Drop table
DROP TABLE IF EXISTS due_accounts;

-- Drop enums
DROP TYPE IF EXISTS due_account_type;
DROP TYPE IF EXISTS due_account_status;

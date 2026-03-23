-- Drop the legacy UNIQUE constraint on bridge_customer_id (was due_account_id).
-- A customer can have multiple virtual accounts (one per currency).
ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_due_account_id_key;

-- Add proper uniqueness: one virtual account per user per currency
CREATE UNIQUE INDEX IF NOT EXISTS idx_virtual_accounts_user_currency ON virtual_accounts(user_id, currency);

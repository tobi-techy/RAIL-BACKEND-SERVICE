-- Drop legacy UNIQUE constraints from migration 015 (Due/Alpaca era).
-- A Bridge customer can have multiple virtual accounts (one per currency).

-- 1. UNIQUE on bridge_customer_id (was due_account_id)
ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_due_account_id_key;

-- 2. UNIQUE on (user_id, alpaca_account_id) — Alpaca not used for Bridge VAs
ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_user_id_alpaca_account_id_key;

-- 3. UNIQUE on account_number — different currencies have different account numbers
ALTER TABLE virtual_accounts DROP CONSTRAINT IF EXISTS virtual_accounts_account_number_key;

-- 4. Make alpaca_account_id and account_number nullable (Bridge VAs don't use Alpaca)
ALTER TABLE virtual_accounts ALTER COLUMN alpaca_account_id DROP NOT NULL;
ALTER TABLE virtual_accounts ALTER COLUMN account_number DROP NOT NULL;

-- 5. Add proper uniqueness: one virtual account per user per currency
CREATE UNIQUE INDEX IF NOT EXISTS idx_virtual_accounts_user_currency ON virtual_accounts(user_id, currency);

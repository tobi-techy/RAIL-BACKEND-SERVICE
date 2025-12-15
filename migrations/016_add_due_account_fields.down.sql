-- Remove Due webhook events table
DROP TABLE IF EXISTS due_webhook_events;

-- Remove Due fields from virtual accounts
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS chain;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS deposit_address;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS due_recipient_id;

-- Remove Due fields from users
DROP INDEX IF EXISTS idx_users_due_account_id;
ALTER TABLE users DROP COLUMN IF EXISTS due_kyc_link;
ALTER TABLE users DROP COLUMN IF EXISTS due_kyc_status;
ALTER TABLE users DROP COLUMN IF EXISTS due_account_id;

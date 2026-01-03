-- Remove all Due-related columns and tables, rename to Bridge where needed

-- Users table: rename due columns to bridge
ALTER TABLE users RENAME COLUMN due_account_id TO bridge_customer_id;
ALTER TABLE users RENAME COLUMN due_kyc_status TO bridge_kyc_status;
ALTER TABLE users RENAME COLUMN due_kyc_link TO bridge_kyc_link;
DROP INDEX IF EXISTS idx_users_due_account_id;
CREATE INDEX IF NOT EXISTS idx_users_bridge_customer_id ON users(bridge_customer_id);

-- Virtual accounts: rename due_account_id to bridge_customer_id, drop due_recipient_id
ALTER TABLE virtual_accounts RENAME COLUMN due_account_id TO bridge_customer_id;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS due_recipient_id;

-- Withdrawals: rename due columns to bridge
ALTER TABLE withdrawals RENAME COLUMN due_transfer_id TO bridge_transfer_id;
ALTER TABLE withdrawals RENAME COLUMN due_recipient_id TO bridge_recipient_id;
UPDATE withdrawals SET status = 'bridge_processing' WHERE status = 'due_processing';

-- Drop Due webhook events table
DROP TABLE IF EXISTS due_webhook_events;

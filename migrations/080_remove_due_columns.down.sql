-- Revert: restore Due columns (for rollback only)

-- Users table
ALTER TABLE users RENAME COLUMN bridge_customer_id TO due_account_id;
ALTER TABLE users RENAME COLUMN bridge_kyc_status TO due_kyc_status;
ALTER TABLE users RENAME COLUMN bridge_kyc_link TO due_kyc_link;
DROP INDEX IF EXISTS idx_users_bridge_customer_id;
CREATE INDEX IF NOT EXISTS idx_users_due_account_id ON users(due_account_id);

-- Virtual accounts
ALTER TABLE virtual_accounts RENAME COLUMN bridge_customer_id TO due_account_id;
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS due_recipient_id VARCHAR(255);

-- Withdrawals
ALTER TABLE withdrawals RENAME COLUMN bridge_transfer_id TO due_transfer_id;
ALTER TABLE withdrawals RENAME COLUMN bridge_recipient_id TO due_recipient_id;
UPDATE withdrawals SET status = 'due_processing' WHERE status = 'bridge_processing';

-- Recreate Due webhook events table
CREATE TABLE IF NOT EXISTS due_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(100) NOT NULL,
    event_data JSONB NOT NULL,
    processed BOOLEAN DEFAULT FALSE,
    processed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

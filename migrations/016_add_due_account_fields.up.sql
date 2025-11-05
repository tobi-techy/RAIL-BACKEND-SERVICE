-- Add Due account fields to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS due_account_id VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS due_kyc_status VARCHAR(50) DEFAULT 'pending';
ALTER TABLE users ADD COLUMN IF NOT EXISTS due_kyc_link TEXT;

-- Add index for Due account lookups
CREATE INDEX IF NOT EXISTS idx_users_due_account_id ON users(due_account_id);

-- Add Due recipient ID to virtual accounts
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS due_recipient_id VARCHAR(255);
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS deposit_address TEXT;
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS chain VARCHAR(50);

-- Create table for Due webhook events
CREATE TABLE IF NOT EXISTS due_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(100) NOT NULL,
    event_data JSONB NOT NULL,
    processed BOOLEAN DEFAULT FALSE,
    processed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_due_webhook_events_type ON due_webhook_events(event_type);
CREATE INDEX IF NOT EXISTS idx_due_webhook_events_processed ON due_webhook_events(processed);

-- Add Due and Alpaca account IDs to users table
ALTER TABLE users 
ADD COLUMN IF NOT EXISTS due_account_id VARCHAR(255),
ADD COLUMN IF NOT EXISTS alpaca_account_id VARCHAR(255);

-- Add indexes for account lookups
CREATE INDEX IF NOT EXISTS idx_users_due_account_id ON users(due_account_id) WHERE due_account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_alpaca_account_id ON users(alpaca_account_id) WHERE alpaca_account_id IS NOT NULL;
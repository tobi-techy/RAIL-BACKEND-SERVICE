-- Add bridge_account_id column to virtual_accounts table
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS bridge_account_id VARCHAR(255);

-- Create index for Bridge account lookups
CREATE INDEX IF NOT EXISTS idx_virtual_accounts_bridge_account_id ON virtual_accounts(bridge_account_id);

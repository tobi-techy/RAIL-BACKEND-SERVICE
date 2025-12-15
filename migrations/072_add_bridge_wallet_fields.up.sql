-- Add Bridge wallet support fields to managed_wallets table
ALTER TABLE managed_wallets 
ADD COLUMN IF NOT EXISTS bridge_wallet_id VARCHAR(255),
ADD COLUMN IF NOT EXISTS provider VARCHAR(50) DEFAULT 'circle';

-- Create index for Bridge wallet lookups
CREATE INDEX IF NOT EXISTS idx_managed_wallets_bridge_wallet_id ON managed_wallets(bridge_wallet_id) WHERE bridge_wallet_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_managed_wallets_provider ON managed_wallets(provider);

-- Add Bridge customer ID to users table for mapping
ALTER TABLE users
ADD COLUMN IF NOT EXISTS bridge_customer_id VARCHAR(255);

CREATE INDEX IF NOT EXISTS idx_users_bridge_customer_id ON users(bridge_customer_id) WHERE bridge_customer_id IS NOT NULL;

-- Update existing wallets to have circle provider
UPDATE managed_wallets SET provider = 'circle' WHERE provider IS NULL;

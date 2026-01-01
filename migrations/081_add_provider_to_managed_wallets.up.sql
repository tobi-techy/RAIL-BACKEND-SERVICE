-- Add provider column to managed_wallets table
-- Supports 'circle' (default) and 'grid' providers

ALTER TABLE managed_wallets 
ADD COLUMN IF NOT EXISTS provider VARCHAR(32) DEFAULT 'circle';

-- Update existing wallets to have 'circle' provider
UPDATE managed_wallets SET provider = 'circle' WHERE provider IS NULL;

-- Make circle_wallet_id nullable for non-Circle wallets (e.g., Grid)
ALTER TABLE managed_wallets 
ALTER COLUMN circle_wallet_id DROP NOT NULL;

-- Add index for provider lookups
CREATE INDEX IF NOT EXISTS idx_managed_wallets_provider ON managed_wallets(provider);

-- Add comment
COMMENT ON COLUMN managed_wallets.provider IS 'Wallet provider: circle, grid';

-- Revert provider column changes

DROP INDEX IF EXISTS idx_managed_wallets_provider;

ALTER TABLE managed_wallets 
DROP COLUMN IF EXISTS provider;

-- Note: Cannot easily restore NOT NULL constraint on circle_wallet_id
-- as existing data may have NULL values after migration

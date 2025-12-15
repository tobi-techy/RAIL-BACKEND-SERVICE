-- Remove Bridge wallet support fields
DROP INDEX IF EXISTS idx_managed_wallets_bridge_wallet_id;
DROP INDEX IF EXISTS idx_managed_wallets_provider;
DROP INDEX IF EXISTS idx_users_bridge_customer_id;

ALTER TABLE managed_wallets 
DROP COLUMN IF EXISTS bridge_wallet_id,
DROP COLUMN IF EXISTS provider;

ALTER TABLE users
DROP COLUMN IF EXISTS bridge_customer_id;

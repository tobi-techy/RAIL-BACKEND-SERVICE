-- Drop UNIQUE on circle_wallet_id so EVM chains can share one Circle wallet
-- (createEVMWallets batches all EVM chains into a single Circle API call,
--  storing multiple rows with the same circle_wallet_id but different chains)
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS managed_wallets_circle_wallet_id_key;
-- Also handle if it was created as a unique index instead of a constraint
DROP INDEX IF EXISTS managed_wallets_circle_wallet_id_key;

-- Re-create as a regular (non-unique) index for query performance
DROP INDEX IF EXISTS idx_managed_wallets_circle_id;
CREATE INDEX idx_managed_wallets_circle_id ON managed_wallets(circle_wallet_id);

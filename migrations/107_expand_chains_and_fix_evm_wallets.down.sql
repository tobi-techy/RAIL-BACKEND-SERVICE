-- Restore UNIQUE on circle_wallet_id
DROP INDEX IF EXISTS idx_managed_wallets_circle_id;
ALTER TABLE managed_wallets ADD CONSTRAINT managed_wallets_circle_wallet_id_key UNIQUE (circle_wallet_id);

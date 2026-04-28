-- Revert: restore original UNIQUE(user_id, chain) constraint
DROP INDEX IF EXISTS idx_managed_wallets_user_chain_type;

-- Only safe if no duplicate (user_id, chain) rows exist
ALTER TABLE managed_wallets ADD CONSTRAINT managed_wallets_user_id_chain_key UNIQUE (user_id, chain);

-- Allow a user to have both a custody wallet and a liquidation address for the same chain.
-- The original UNIQUE(user_id, chain) prevents this. Replace with a unique index that
-- includes account_type so each (user, chain, account_type) combination is unique.
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS managed_wallets_user_id_chain_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_managed_wallets_user_chain_type
    ON managed_wallets(user_id, chain, account_type);

-- Fix managed_wallets chain constraint to support only mainnet chains
-- Removes testnet chains and adds BASE, AVAX
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL', 'MATIC', 'CELO', 'TRON', 'BASE', 'AVAX')
    );

-- Create composite index for common queries (user_id, chain, status)
CREATE INDEX IF NOT EXISTS idx_managed_wallets_user_chain_status 
    ON managed_wallets(user_id, chain, status);

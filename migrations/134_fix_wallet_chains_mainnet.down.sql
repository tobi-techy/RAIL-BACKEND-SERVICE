-- Revert chain constraint changes
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL-DEVNET', 'SOL', 'MATIC-AMOY', 'MATIC', 'CELO-ALFAJORES', 'CELO', 'TRON-SHASTA', 'TRON')
    );

-- Drop the composite index if it exists
DROP INDEX IF EXISTS idx_managed_wallets_user_chain_status;

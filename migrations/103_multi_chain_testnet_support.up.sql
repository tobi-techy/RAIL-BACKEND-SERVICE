-- Expand managed_wallets chain constraint to support MATIC-AMOY and AVAX-FUJI testnets
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL-DEVNET', 'MATIC-AMOY', 'AVAX-FUJI')
    );

-- Remove hardcoded SOL default from bridge_transactions dest_chain
ALTER TABLE bridge_transactions ALTER COLUMN dest_chain DROP DEFAULT;

-- Revert to previous chain constraint without CELO/TRON
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL-DEVNET', 'SOL', 'MATIC-AMOY', 'MATIC', 'AVAX-FUJI', 'AVAX')
    );

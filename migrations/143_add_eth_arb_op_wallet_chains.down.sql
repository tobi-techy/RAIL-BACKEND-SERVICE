-- Revert: remove ETH, ARB, OP from managed_wallets chain constraint
ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL', 'MATIC', 'CELO', 'TRON', 'BASE', 'AVAX')
    );

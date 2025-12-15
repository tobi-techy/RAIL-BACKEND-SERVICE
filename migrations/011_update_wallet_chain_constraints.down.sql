-- Revert managed_wallets chain constraint to previous definition
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_chain;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN (
            'ETH', 'ETH-SEPOLIA',
            'MATIC',
            'AVAX',
            'SOL', 'SOL-DEVNET',
            'APTOS', 'APTOS-TESTNET'
        )
    );

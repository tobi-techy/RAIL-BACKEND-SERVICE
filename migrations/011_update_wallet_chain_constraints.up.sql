-- Update managed_wallets chain constraint to include Polygon and Base networks
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_chain;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN (
            'ETH', 'ETH-SEPOLIA',
            'MATIC', 'MATIC-AMOY',
            'AVAX',
            'SOL', 'SOL-DEVNET',
            'APTOS', 'APTOS-TESTNET',
            'BASE', 'BASE-SEPOLIA'
        )
    );

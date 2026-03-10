-- Expand deposits.chain constraint to supported chains only
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_check;
ALTER TABLE deposits
    ADD CONSTRAINT deposits_chain_check CHECK (
        chain IN (
            'SOL', 'SOL-DEVNET',
            'MATIC', 'MATIC-AMOY',
            'AVAX', 'AVAX-FUJI',
            'BASE', 'BASE-SEPOLIA'
        )
    );

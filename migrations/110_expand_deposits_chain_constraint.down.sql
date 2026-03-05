ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_check;
ALTER TABLE deposits
    ADD CONSTRAINT deposits_chain_check CHECK (
        chain IN ('ETH', 'ETH-SEPOLIA', 'SOL', 'SOL-DEVNET', 'APTOS', 'APTOS-TESTNET')
    );

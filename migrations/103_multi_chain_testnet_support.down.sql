ALTER TABLE managed_wallets DROP CONSTRAINT IF EXISTS chk_wallet_chain;
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (chain IN ('SOL-DEVNET'));

ALTER TABLE bridge_transactions ALTER COLUMN dest_chain SET DEFAULT 'SOL';

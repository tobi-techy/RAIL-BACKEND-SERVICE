-- Data migration: coerce any new account types back to EOA before restoring the constraint
UPDATE managed_wallets SET account_type = 'EOA' WHERE account_type NOT IN ('EOA', 'SCA');

-- Now safe to drop and recreate the constraint
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_account_type;
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS managed_wallets_account_type_check;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_account_type CHECK (
        account_type IN ('EOA', 'SCA')
    );

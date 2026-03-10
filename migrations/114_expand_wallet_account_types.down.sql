-- Safety check: refuse rollback if any wallets with new account types exist.
-- Manual steps required: 1) Backup wallet data, 2) Migrate wallets to appropriate
-- legacy types, 3) Verify business logic compatibility, 4) Re-run rollback.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM managed_wallets WHERE account_type IN ('bridge_wallet', 'liquidation_address')) THEN
        RAISE EXCEPTION 'Cannot rollback: wallets with new account types exist. Manual migration required: 1) Backup wallet data, 2) Migrate wallets to appropriate legacy types, 3) Verify business logic compatibility, 4) Re-run rollback.';
    END IF;
END $$;

ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_account_type;
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS managed_wallets_account_type_check;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_account_type CHECK (
        account_type IN ('EOA', 'SCA')
    );

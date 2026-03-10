-- Safety check: refuse rollback if any wallets with new account types exist
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM managed_wallets WHERE account_type IN ('bridge_wallet', 'liquidation_address')) THEN
        RAISE EXCEPTION 'Cannot rollback: wallets with new account types exist. Migrate these wallets to Circle before rolling back.';
    END IF;
END $$;

-- Revert managed_wallets.account_type constraint to legacy values
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_account_type;
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS managed_wallets_account_type_check;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_account_type CHECK (
        account_type IN ('EOA', 'SCA')
    );

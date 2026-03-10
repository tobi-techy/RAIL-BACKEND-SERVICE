-- Expand managed_wallets.account_type to support Bridge wallet types
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_account_type;
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS managed_wallets_account_type_check;

ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_account_type CHECK (
        account_type IN ('EOA', 'SCA', 'bridge_wallet', 'liquidation_address')
    );

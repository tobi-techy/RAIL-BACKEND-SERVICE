ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'fiat_exposure', 'pending_investment',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'
));

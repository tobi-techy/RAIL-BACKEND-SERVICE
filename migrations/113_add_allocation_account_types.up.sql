-- Add spending_balance and stash_balance account types used by the allocation service.
-- These were missing from the constraint, causing GetOrCreateUserAccount to fail silently.
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'
));

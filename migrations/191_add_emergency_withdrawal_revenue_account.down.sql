DELETE FROM ledger_accounts WHERE account_type = 'emergency_withdrawal_revenue' AND user_id IS NULL AND balance = 0;
DELETE FROM ledger_accounts WHERE account_type = 'subscription_revenue' AND user_id IS NULL AND balance = 0;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'
));

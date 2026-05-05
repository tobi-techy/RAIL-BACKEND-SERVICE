-- Add revenue account types to the account_type constraint and seed system accounts.

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'emergency_withdrawal_revenue'
));

INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT gen_random_uuid(), NULL, 'emergency_withdrawal_revenue', 'USD', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts WHERE account_type = 'emergency_withdrawal_revenue' AND user_id IS NULL
);

INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT gen_random_uuid(), NULL, 'subscription_revenue', 'USD', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts WHERE account_type = 'subscription_revenue' AND user_id IS NULL
);

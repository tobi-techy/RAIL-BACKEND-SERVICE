-- Add pending_card_settlement account type for card authorization holds
-- and card_hold transaction type for hold/release ledger entries.
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'
));

ALTER TABLE ledger_transactions DROP CONSTRAINT IF EXISTS chk_transaction_type;
ALTER TABLE ledger_transactions ADD CONSTRAINT chk_transaction_type CHECK (transaction_type IN (
    'deposit', 'withdrawal', 'investment', 'conversion',
    'internal_transfer', 'buffer_replenishment', 'reversal',
    'card_payment', 'card_hold', 'p2p_transfer'
));

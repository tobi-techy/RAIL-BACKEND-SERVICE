-- Revert goal_balance sub-account support

DROP INDEX IF EXISTS idx_ledger_accounts_user_goal_type;
DROP INDEX IF EXISTS idx_ledger_accounts_user_goal;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_non_goal_no_goal_id;
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_goal_balance_has_goal;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue'
)) NOT VALID;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_account_type;

ALTER TABLE ledger_accounts DROP COLUMN IF EXISTS goal_id;

-- Reverse the early-retirement penalty revenue account.

DELETE FROM ledger_accounts
WHERE user_id IS NULL AND account_type = 'early_retirement_penalty_revenue';

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue',
          'limit_increase_revenue'
      );

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue',
    'limit_increase_revenue', 'goal_balance'
)) NOT VALID;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_account_type;

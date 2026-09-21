-- System revenue account for the 10% early-retirement earnings haircut.
-- Mirrors 191/207: extend the CHECK, seed the singleton system account, and
-- refresh the one-per-system-type index.

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue',
    'limit_increase_revenue', 'goal_balance', 'early_retirement_penalty_revenue'
)) NOT VALID;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_account_type;

INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT gen_random_uuid(), NULL, 'early_retirement_penalty_revenue', 'USDC', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts
    WHERE user_id IS NULL AND account_type = 'early_retirement_penalty_revenue'
);

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue',
          'limit_increase_revenue', 'early_retirement_penalty_revenue'
      );

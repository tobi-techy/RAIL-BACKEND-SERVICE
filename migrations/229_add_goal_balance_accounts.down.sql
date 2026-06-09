-- Revert goal_balance sub-account support

DROP INDEX IF EXISTS idx_ledger_accounts_user_goal_type;
DROP INDEX IF EXISTS idx_ledger_accounts_user_goal;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_non_goal_no_goal_id;
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_goal_balance_has_goal;

-- Clean up any goal_balance accounts before removing the type from the constraint.
-- First, ensure every user with goal balances has a stash_balance account to merge into.
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
SELECT gen_random_uuid(), g.user_id, 'stash_balance', 'USDC', 0, NOW(), NOW()
FROM (SELECT DISTINCT user_id FROM ledger_accounts WHERE account_type = 'goal_balance') g
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts la
    WHERE la.user_id = g.user_id AND la.account_type = 'stash_balance'
);

-- Merge goal balances back into existing stash accounts, then delete goal rows.
UPDATE ledger_accounts stash
SET balance = stash.balance + goal.total
FROM (
    SELECT user_id, SUM(balance) AS total
    FROM ledger_accounts
    WHERE account_type = 'goal_balance'
    GROUP BY user_id
) goal
WHERE stash.user_id = goal.user_id AND stash.account_type = 'stash_balance';

DELETE FROM ledger_accounts WHERE account_type = 'goal_balance';

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue'
)) NOT VALID;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_account_type;

ALTER TABLE ledger_accounts DROP COLUMN IF EXISTS goal_id;

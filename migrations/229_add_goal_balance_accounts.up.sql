-- Add goal_balance account type for per-goal earmarked funds within stash.
-- Each savings goal gets its own ledger account so withdrawals don't silently
-- drain goal-allocated money.

-- 1. Add goal_id column (references the shared_goals table)
-- ON DELETE RESTRICT: cannot delete a goal that has a ledger account (must drain funds first)
ALTER TABLE ledger_accounts ADD COLUMN IF NOT EXISTS goal_id UUID REFERENCES shared_goals(id) ON DELETE RESTRICT;

-- 2. Update the account type constraint to include goal_balance
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'withdrawal_fee_revenue', 'emergency_withdrawal_revenue',
    'goal_balance'
)) NOT VALID;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_account_type;

-- 3. Ensure goal_balance accounts always have a goal_id
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_goal_balance_has_goal
    CHECK (account_type != 'goal_balance' OR goal_id IS NOT NULL) NOT VALID;

-- 4. Ensure non-goal accounts don't have a goal_id
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_non_goal_no_goal_id
    CHECK (account_type = 'goal_balance' OR goal_id IS NULL) NOT VALID;

-- 5. Validate goal constraints (separate step avoids long locks on large tables)
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_goal_balance_has_goal;
ALTER TABLE ledger_accounts VALIDATE CONSTRAINT chk_non_goal_no_goal_id;

-- 5. One goal_balance account per user per goal
CREATE UNIQUE INDEX idx_ledger_accounts_user_goal
    ON ledger_accounts(user_id, goal_id)
    WHERE account_type = 'goal_balance' AND user_id IS NOT NULL AND goal_id IS NOT NULL;

-- 6. Index for looking up all goal accounts for a user
CREATE INDEX idx_ledger_accounts_user_goal_type
    ON ledger_accounts(user_id, account_type)
    WHERE account_type = 'goal_balance';

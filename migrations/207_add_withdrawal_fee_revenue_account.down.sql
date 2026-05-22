-- Remove withdrawal fee revenue account type if it has no balance.

DO $$
DECLARE
    revenue_balance DECIMAL;
BEGIN
    SELECT balance INTO revenue_balance
    FROM ledger_accounts
    WHERE user_id IS NULL AND account_type = 'withdrawal_fee_revenue';

    IF NOT FOUND THEN
        RAISE NOTICE 'withdrawal_fee_revenue account does not exist, skipping balance check';
    ELSIF revenue_balance <> 0 THEN
        RAISE EXCEPTION 'withdrawal_fee_revenue account has non-zero balance % and requires manual intervention', revenue_balance;
    END IF;
END $$;

DELETE FROM ledger_accounts
WHERE user_id IS NULL AND account_type = 'withdrawal_fee_revenue';

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'emergency_withdrawal_revenue'
));

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'emergency_withdrawal_revenue'
      );

DO $$
DECLARE
    revenue_account_type TEXT;
    revenue_account_balance DECIMAL;
BEGIN
    SELECT account_type, balance
    INTO revenue_account_type, revenue_account_balance
    FROM ledger_accounts
    WHERE account_type IN ('emergency_withdrawal_revenue', 'subscription_revenue')
      AND user_id IS NULL
      AND balance <> 0
    LIMIT 1;

    IF revenue_account_type IS NOT NULL THEN
        RAISE EXCEPTION 'revenue account % has non-zero balance % and requires manual intervention', revenue_account_type, revenue_account_balance;
    END IF;

    DELETE FROM ledger_accounts
    WHERE account_type IN ('emergency_withdrawal_revenue', 'subscription_revenue')
      AND user_id IS NULL;
END $$;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'
));

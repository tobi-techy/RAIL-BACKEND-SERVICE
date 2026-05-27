-- Fix: Debit $90.73 PITCHERS token deposit incorrectly credited to usdc_balance
-- Date: 2026-05-12
-- Circle Wallet ID: 42e98a94-3d05-5add-a59c-1438b2c3a1de
-- Tx Hash: 25vDpXN8E64xNP5CQuTtircZvYypAHpv6LkpLcL9qedhFbPArxLPz8mnA9U94YmTRbRt8yzkVfNqZeQtib6LBCtN
-- Token: PITCHERS (unsupported)
-- Amount: 90.7284276364

BEGIN;

DO $$
DECLARE
    v_user_id UUID;
    v_account_id UUID;
    v_balance NUMERIC;
    v_amount NUMERIC := 90.7284276364;
BEGIN
    SELECT user_id INTO v_user_id
    FROM managed_wallets
    WHERE circle_wallet_id = '42e98a94-3d05-5add-a59c-1438b2c3a1de'
    LIMIT 1;

    IF v_user_id IS NULL THEN
        RAISE EXCEPTION 'No user found for wallet 42e98a94-3d05-5add-a59c-1438b2c3a1de';
    END IF;

    RAISE NOTICE 'User: %', v_user_id;

    -- Debit from usdc_balance (legacy, not split)
    SELECT id, balance INTO v_account_id, v_balance
    FROM ledger_accounts
    WHERE user_id = v_user_id AND account_type = 'usdc_balance';

    IF v_account_id IS NULL THEN
        RAISE EXCEPTION 'No usdc_balance account found for user %', v_user_id;
    END IF;

    RAISE NOTICE 'Current usdc_balance: %', v_balance;

    IF v_balance < v_amount THEN
        RAISE EXCEPTION 'Insufficient balance: have %, need %', v_balance, v_amount;
    END IF;

    UPDATE ledger_accounts
    SET balance = balance - v_amount, updated_at = NOW()
    WHERE id = v_account_id;

    -- Mark deposit as reversed
    UPDATE deposits
    SET status = 'reversed'
    WHERE tx_hash = '25vDpXN8E64xNP5CQuTtircZvYypAHpv6LkpLcL9qedhFbPArxLPz8mnA9U94YmTRbRt8yzkVfNqZeQtib6LBCtN';

    RAISE NOTICE 'Debited % from usdc_balance, new balance: %', v_amount, v_balance - v_amount;
END $$;

COMMIT;

-- Verify
SELECT account_type, balance
FROM ledger_accounts la
JOIN managed_wallets mw ON la.user_id = mw.user_id
WHERE mw.circle_wallet_id = '42e98a94-3d05-5add-a59c-1438b2c3a1de'
  AND la.account_type IN ('spending_balance', 'stash_balance', 'usdc_balance');

-- One-time ledger adjustment: debit $0.42 to sync ledger with on-chain balance ($2.95).
-- $0.23 bridge dust below transfer minimum + $0.19 chainrails fees from failed withdrawals.
DO $$
DECLARE
    v_account_id UUID;
    v_tx_id UUID;
BEGIN
    SELECT id INTO v_account_id FROM ledger_accounts
    WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478' AND account_type = 'spending_balance';

    IF v_account_id IS NULL THEN RETURN; END IF;

    v_tx_id := gen_random_uuid();

    INSERT INTO ledger_transactions (id, user_id, transaction_type, status, amount, currency, description, idempotency_key, created_at, updated_at)
    VALUES (v_tx_id, 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478', 'reversal', 'completed', 0.42, 'USDC',
            'reconciliation: bridge dust + chainrails fee write-off', 'reconcile-bridge-dust-20260506', NOW(), NOW());

    INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
    VALUES (gen_random_uuid(), v_tx_id, v_account_id, 'debit', 0.42, 'USDC',
            'reconciliation: bridge dust + chainrails fee write-off', NOW());

    UPDATE ledger_accounts SET balance = balance - 0.42, updated_at = NOW() WHERE id = v_account_id;
END $$;

-- Fix withdrawals that were ledger-reversed but still show as 'failed' or 'processing'
UPDATE withdrawals SET status = 'reversed', updated_at = NOW()
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND status IN ('failed', 'processing')
  AND id IN (
    SELECT w.id FROM withdrawals w
    JOIN ledger_transactions lt ON lt.reference_id = w.id::text AND lt.transaction_type = 'reversal'
    WHERE w.user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  );

-- Manual Correction: Reverse erroneous refund for withdrawal 1731307c-e553-4752-8a42-c5acd4ab865a
-- The on-chain transfer (Solana tx: 3ziVWPt2af1K1ysArXBRQQNNSvYvQvxuEvnREb1VUhQBXrWG6NvJWVwp5ie2nUVqoNJ7ELsjG9vaS5NRSM5Vqc98)
-- landed successfully, but a race condition caused the system to also reverse the ledger debit,
-- double-crediting the user 2 USDC.
--
-- This script debits the user's account to reclaim the erroneous refund.

-- Step 0: Verify the withdrawal and identify the user
-- Run this SELECT first to confirm details before executing the transaction block.
SELECT w.id, w.user_id, w.amount, w.fee_amount, w.status, w.source_account, w.tx_hash, w.error_message, w.updated_at
FROM withdrawals w
WHERE w.id = '1731307c-e553-4752-8a42-c5acd4ab865a';

BEGIN;

DO $$
DECLARE
  v_withdrawal_id UUID := '1731307c-e553-4752-8a42-c5acd4ab865a';
  v_user_id UUID;
  v_amount NUMERIC;
  v_fee_amount NUMERIC;
  v_source_account TEXT;
  v_user_account_id UUID;
  v_system_account_id UUID;
  v_tx_id UUID := gen_random_uuid();
  v_account_type TEXT;
  v_total_reversal NUMERIC;
BEGIN
  -- Fetch withdrawal details
  SELECT user_id, amount, fee_amount, source_account
  INTO v_user_id, v_amount, v_fee_amount, v_source_account
  FROM withdrawals
  WHERE id = v_withdrawal_id;

  IF v_user_id IS NULL THEN
    RAISE EXCEPTION 'Withdrawal % not found', v_withdrawal_id;
  END IF;

  -- Determine account type from source
  v_account_type := CASE
    WHEN v_source_account IN ('stash', 'stash_balance') THEN 'stash_balance'
    ELSE 'usdc_balance'
  END;

  v_total_reversal := v_amount + COALESCE(v_fee_amount, 0);

  -- Get user's ledger account
  SELECT id INTO v_user_account_id
  FROM ledger_accounts
  WHERE user_id = v_user_id AND account_type = v_account_type;

  IF v_user_account_id IS NULL THEN
    RAISE EXCEPTION 'User ledger account not found for user % account type %', v_user_id, v_account_type;
  END IF;

  -- Get system buffer account
  SELECT id INTO v_system_account_id
  FROM ledger_accounts
  WHERE user_id IS NULL AND account_type = 'system_buffer_usdc';

  IF v_system_account_id IS NULL THEN
    RAISE EXCEPTION 'System buffer account not found';
  END IF;

  -- Verify the erroneous reversal exists (idempotency key pattern from the reversal logic)
  IF NOT EXISTS (
    SELECT 1 FROM ledger_transactions
    WHERE idempotency_key = 'withdrawal-reversal-' || v_withdrawal_id::text
      AND user_id = v_user_id
  ) THEN
    RAISE EXCEPTION 'No reversal transaction found for withdrawal % — may have already been corrected', v_withdrawal_id;
  END IF;

  -- Verify user has sufficient balance
  IF (SELECT balance FROM ledger_accounts WHERE id = v_user_account_id) < v_total_reversal THEN
    RAISE EXCEPTION 'User balance insufficient to reclaim %. Current balance: %',
      v_total_reversal,
      (SELECT balance FROM ledger_accounts WHERE id = v_user_account_id);
  END IF;

  -- Create corrective ledger transaction
  INSERT INTO ledger_transactions (id, user_id, transaction_type, reference_id, reference_type, idempotency_key, description, metadata, created_at)
  VALUES (
    v_tx_id,
    v_user_id,
    'correction',
    v_withdrawal_id,
    'withdrawal',
    'correction-erroneous-refund-' || v_withdrawal_id::text,
    'Correction: reverse erroneous refund for confirmed on-chain withdrawal',
    jsonb_build_object(
      'correction_reason', 'race_condition_double_credit',
      'withdrawal_id', v_withdrawal_id::text,
      'on_chain_tx', '3ziVWPt2af1K1ysArXBRQQNNSvYvQvxuEvnREb1VUhQBXrWG6NvJWVwp5ie2nUVqoNJ7ELsjG9vaS5NRSM5Vqc98',
      'manual_correction', true
    ),
    NOW()
  );

  -- Credit user account (reduces their balance)
  INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
  VALUES (gen_random_uuid(), v_tx_id, v_user_account_id, 'credit', v_total_reversal, 'USDC', 'Correction: reclaim erroneous refund', NOW());

  -- Debit system buffer (restores system balance)
  INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
  VALUES (gen_random_uuid(), v_tx_id, v_system_account_id, 'debit', v_total_reversal, 'USDC', 'Correction: reclaim erroneous refund', NOW());

  -- Update account balances
  UPDATE ledger_accounts SET balance = balance - v_total_reversal, updated_at = NOW() WHERE id = v_user_account_id;
  UPDATE ledger_accounts SET balance = balance + v_total_reversal, updated_at = NOW() WHERE id = v_system_account_id;

  -- Fix the withdrawal status: mark as completed since on-chain tx succeeded
  UPDATE withdrawals
  SET status = 'completed',
      error_message = NULL,
      tx_hash = '3ziVWPt2af1K1ysArXBRQQNNSvYvQvxuEvnREb1VUhQBXrWG6NvJWVwp5ie2nUVqoNJ7ELsjG9vaS5NRSM5Vqc98',
      completed_at = NOW(),
      updated_at = NOW()
  WHERE id = v_withdrawal_id;

  RAISE NOTICE 'Successfully corrected erroneous refund of % USDC for user %', v_total_reversal, v_user_id;
  RAISE NOTICE 'Withdrawal % marked as completed', v_withdrawal_id;
  RAISE NOTICE 'New user balance: %', (SELECT balance FROM ledger_accounts WHERE id = v_user_account_id);
END $$;

COMMIT;

-- Post-verification
SELECT 'withdrawal_status' AS check, status::text AS value FROM withdrawals WHERE id = '1731307c-e553-4752-8a42-c5acd4ab865a'
UNION ALL
SELECT 'user_balance', balance::text FROM ledger_accounts
WHERE user_id = (SELECT user_id FROM withdrawals WHERE id = '1731307c-e553-4752-8a42-c5acd4ab865a')
  AND account_type IN ('usdc_balance', 'stash_balance');

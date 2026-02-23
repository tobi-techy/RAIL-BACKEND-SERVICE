-- Manual Recovery Script for Lost 40 USDC Deposits
-- Run this in your staging database (Neon)

-- User and wallet info
-- User ID: 59d1e9e9-dbce-4bd3-840d-18f731dc285b
-- Wallet: Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ

BEGIN;

-- Step 1: Create ledger accounts if they don't exist
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
VALUES (
  gen_random_uuid(),
  '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
  'usdc_balance',
  'USDC',
  0
)
ON CONFLICT (user_id, account_type) WHERE user_id IS NOT NULL DO NOTHING;

-- Get the user account ID
DO $$
DECLARE
  v_user_account_id UUID;
  v_system_account_id UUID;
  v_deposit1_id UUID := gen_random_uuid();
  v_deposit2_id UUID := gen_random_uuid();
  v_tx1_id UUID := gen_random_uuid();
  v_tx2_id UUID := gen_random_uuid();
BEGIN
  -- Get user USDC account
  SELECT id INTO v_user_account_id
  FROM ledger_accounts
  WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b'
    AND account_type = 'usdc_balance';

  -- Get system buffer account (create if doesn't exist)
  SELECT id INTO v_system_account_id
  FROM ledger_accounts
  WHERE user_id IS NULL
    AND account_type = 'system_buffer_usdc';
  
  IF v_system_account_id IS NULL THEN
    INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
    VALUES (gen_random_uuid(), NULL, 'system_buffer_usdc', 'USDC', 1000000)
    RETURNING id INTO v_system_account_id;
  END IF;

  -- Deposit 1: First 20 USDC
  INSERT INTO deposits (id, user_id, chain, tx_hash, token, amount, status, confirmed_at, created_at)
  VALUES (
    v_deposit1_id,
    '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
    'Solana',
    '4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU',
    'USDC',
    20.00,
    'confirmed',
    NOW(),
    NOW()
  );

  -- Deposit 2: Second 20 USDC (you'll need to provide the actual tx hash)
  INSERT INTO deposits (id, user_id, chain, tx_hash, token, amount, status, confirmed_at, created_at)
  VALUES (
    v_deposit2_id,
    '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
    'Solana',
    '5kW5S7Q3mQfVEt1o2Po6Nw1U1CqfjLtv2rAC5mLq1Y74uibrfR3agJKkc3cEXN5Ydrtyj6A8GNKqF5LLqQPJhsPy',  -- ⚠️ UPDATE THIS
    'USDC',
    20.00,
    'confirmed',
    NOW(),
    NOW()
  );

  -- Ledger Transaction 1: First 20 USDC
  INSERT INTO ledger_transactions (id, user_id, transaction_type, reference_id, reference_type, description, metadata, created_at)
  VALUES (
    v_tx1_id,
    '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
    'deposit',
    v_deposit1_id,
    'deposit',
    'USDC deposit from SOL: 4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU',
    jsonb_build_object('chain', 'Solana', 'tx_hash', '4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU', 'manual_recovery', true),
    NOW()
  );

  -- Ledger entries for deposit 1
  INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
  VALUES
    (gen_random_uuid(), v_tx1_id, v_user_account_id, 'debit', 20.00, 'USDC', 'Deposit 20 USDC', NOW()),
    (gen_random_uuid(), v_tx1_id, v_system_account_id, 'credit', 20.00, 'USDC', 'Deposit 20 USDC', NOW());

  -- Ledger Transaction 2: Second 20 USDC
  INSERT INTO ledger_transactions (id, user_id, transaction_type, reference_id, reference_type, description, metadata, created_at)
  VALUES (
    v_tx2_id,
    '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
    'deposit',
    v_deposit2_id,
    'deposit',
    'USDC deposit from SOL: 5kW5S7Q3mQfVEt1o2Po6Nw1U1CqfjLtv2rAC5mLq1Y74uibrfR3agJKkc3cEXN5Ydrtyj6A8GNKqF5LLqQPJhsPy',  -- ⚠️ UPDATE THIS
    jsonb_build_object('chain', 'Solana', 'tx_hash', '5kW5S7Q3mQfVEt1o2Po6Nw1U1CqfjLtv2rAC5mLq1Y74uibrfR3agJKkc3cEXN5Ydrtyj6A8GNKqF5LLqQPJhsPy', 'manual_recovery', true),  -- ⚠️ UPDATE THIS
    NOW()
  );

  -- Ledger entries for deposit 2
  INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
  VALUES
    (gen_random_uuid(), v_tx2_id, v_user_account_id, 'debit', 20.00, 'USDC', 'Deposit 20 USDC', NOW()),
    (gen_random_uuid(), v_tx2_id, v_system_account_id, 'credit', 20.00, 'USDC', 'Deposit 20 USDC', NOW());

  -- Update account balances
  UPDATE ledger_accounts
  SET balance = balance + 40.00,
      updated_at = NOW()
  WHERE id = v_user_account_id;

  UPDATE ledger_accounts
  SET balance = balance - 40.00,
      updated_at = NOW()
  WHERE id = v_system_account_id;

  RAISE NOTICE 'Successfully recovered 40 USDC for user 59d1e9e9-dbce-4bd3-840d-18f731dc285b';
  RAISE NOTICE 'User account balance: %', (SELECT balance FROM ledger_accounts WHERE id = v_user_account_id);
END $$;

COMMIT;

-- Verify the recovery
SELECT 'Deposits' as table_name, COUNT(*) as count FROM deposits WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b'
UNION ALL
SELECT 'Ledger Accounts', COUNT(*) FROM ledger_accounts WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b'
UNION ALL
SELECT 'Ledger Transactions', COUNT(*) FROM ledger_transactions WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b'
UNION ALL
SELECT 'User Balance', balance::text::numeric FROM ledger_accounts WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b' AND account_type = 'usdc_balance';

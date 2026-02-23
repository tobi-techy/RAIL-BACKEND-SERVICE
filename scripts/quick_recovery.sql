-- Quick 40 USDC Recovery Script
BEGIN;

-- Create user USDC account
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
VALUES (gen_random_uuid(), '59d1e9e9-dbce-4bd3-840d-18f731dc285b', 'usdc_balance', 'USDC', 40.00)
ON CONFLICT (user_id, account_type) WHERE user_id IS NOT NULL 
DO UPDATE SET balance = ledger_accounts.balance + 40.00, updated_at = NOW();

-- Create system buffer if needed
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
VALUES (gen_random_uuid(), NULL, 'system_buffer_usdc', 'USDC', 1000000)
ON CONFLICT (account_type) WHERE user_id IS NULL AND account_type = 'system_buffer_usdc'
DO NOTHING;

-- Deposit 1
INSERT INTO deposits (id, user_id, chain, tx_hash, token, amount, status, confirmed_at, created_at)
VALUES (
  gen_random_uuid(),
  '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
  'Solana',
  '4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU',
  'USDC',
  20.00,
  'confirmed',
  NOW(),
  NOW()
);

-- Deposit 2
INSERT INTO deposits (id, user_id, chain, tx_hash, token, amount, status, confirmed_at, created_at)
VALUES (
  gen_random_uuid(),
  '59d1e9e9-dbce-4bd3-840d-18f731dc285b',
  'Solana',
  '5kW5S7Q3mQfVEt1o2Po6Nw1U1CqfjLtv2rAC5mLq1Y74uibrfR3agJKkc3cEXN5Ydrtyj6A8GNKqF5LLqQPJhsPy',
  'USDC',
  20.00,
  'confirmed',
  NOW(),
  NOW()
);

COMMIT;

-- Verify
SELECT 'User Balance' as info, balance FROM ledger_accounts 
WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b' AND account_type = 'usdc_balance'
UNION ALL
SELECT 'Deposits', COUNT(*)::numeric FROM deposits WHERE user_id = '59d1e9e9-dbce-4bd3-840d-18f731dc285b';

-- Reverse the dust adjustment
UPDATE ledger_accounts
SET balance = balance + 0.23, updated_at = NOW()
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND account_type = 'spending_balance';

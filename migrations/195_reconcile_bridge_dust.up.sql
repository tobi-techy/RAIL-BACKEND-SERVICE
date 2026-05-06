-- One-time ledger adjustment: debit $0.23 bridge dust from user spending_balance.
-- This amount is stuck in a Bridge Solana wallet below the $1 transfer minimum.
UPDATE ledger_accounts
SET balance = balance - 0.23, updated_at = NOW()
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND account_type = 'spending_balance'
  AND balance >= 0.23;

INSERT INTO ledger_entries (id, account_id, amount, entry_type, description, created_at)
SELECT gen_random_uuid(), id, -0.23, 'debit', 'reconciliation: bridge wallet dust below transfer minimum', NOW()
FROM ledger_accounts
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND account_type = 'spending_balance';

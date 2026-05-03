-- Seed the emergency_withdrawal_revenue system account for early stash withdrawal fees
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT gen_random_uuid(), NULL, 'emergency_withdrawal_revenue', 'USD', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts WHERE account_type = 'emergency_withdrawal_revenue' AND user_id IS NULL
);

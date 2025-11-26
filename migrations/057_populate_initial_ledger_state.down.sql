-- 057_populate_initial_ledger_state.down.sql
-- Rollback script to remove all data created by the migration

-- Delete all user-created ledger entries (keep only system accounts)
DELETE FROM ledger_entries 
WHERE transaction_id IN (
    SELECT id FROM ledger_transactions 
    WHERE idempotency_key LIKE 'migration_057_%'
);

-- Delete all migration transactions
DELETE FROM ledger_transactions 
WHERE idempotency_key LIKE 'migration_057_%';

-- Delete all user ledger accounts (keep only system accounts)
DELETE FROM ledger_accounts 
WHERE user_id IS NOT NULL;

-- Reset system account balances to zero
UPDATE ledger_accounts 
SET balance = 0, updated_at = NOW()
WHERE account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational');

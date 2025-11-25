-- Migration Rollback: Drop Ledger Tables

-- Drop triggers first
DROP TRIGGER IF EXISTS validate_ledger_entries_balance ON ledger_entries;
DROP TRIGGER IF EXISTS update_ledger_accounts_updated_at ON ledger_accounts;

-- Drop functions
DROP FUNCTION IF EXISTS validate_ledger_balance();

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS ledger_entries CASCADE;
DROP TABLE IF EXISTS ledger_transactions CASCADE;
DROP TABLE IF EXISTS ledger_accounts CASCADE;

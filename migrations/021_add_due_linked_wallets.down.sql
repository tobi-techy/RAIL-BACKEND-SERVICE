-- Rollback: Drop due_linked_wallets table and related indexes

DROP INDEX IF EXISTS idx_due_linked_wallets_blockchain;
DROP INDEX IF EXISTS idx_due_linked_wallets_status;
DROP INDEX IF EXISTS idx_due_linked_wallets_managed_wallet;
DROP INDEX IF EXISTS idx_due_linked_wallets_user;
DROP INDEX IF EXISTS idx_due_linked_wallets_due_account;

DROP TABLE IF EXISTS due_linked_wallets;

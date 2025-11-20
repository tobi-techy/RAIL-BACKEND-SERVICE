-- Drop performance indexes

DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS idx_users_phone;
DROP INDEX IF EXISTS idx_users_created_at;

DROP INDEX IF EXISTS idx_wallets_user_id;
DROP INDEX IF EXISTS idx_wallets_chain;
DROP INDEX IF EXISTS idx_wallets_address;
DROP INDEX IF EXISTS idx_wallets_status;

DROP INDEX IF EXISTS idx_deposits_user_id;
DROP INDEX IF EXISTS idx_deposits_status;
DROP INDEX IF EXISTS idx_deposits_chain;
DROP INDEX IF EXISTS idx_deposits_tx_hash;
DROP INDEX IF EXISTS idx_deposits_created_at;

DROP INDEX IF EXISTS idx_balances_user_id;
DROP INDEX IF EXISTS idx_balances_updated_at;

DROP INDEX IF EXISTS idx_transactions_user_id;
DROP INDEX IF EXISTS idx_transactions_type;
DROP INDEX IF EXISTS idx_transactions_status;
DROP INDEX IF EXISTS idx_transactions_created_at;

DROP INDEX IF EXISTS idx_onboarding_flows_user_id;
DROP INDEX IF EXISTS idx_onboarding_flows_status;

DROP INDEX IF EXISTS idx_audit_logs_user_id;
DROP INDEX IF EXISTS idx_audit_logs_action;
DROP INDEX IF EXISTS idx_audit_logs_created_at;

DROP INDEX IF EXISTS idx_deposits_user_status;
DROP INDEX IF EXISTS idx_wallets_user_chain;
DROP INDEX IF EXISTS idx_transactions_user_type;

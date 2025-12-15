-- Drop wallet tables and indexes (in reverse dependency order)
DROP INDEX IF EXISTS idx_wallet_provisioning_jobs_retry;
DROP INDEX IF EXISTS idx_wallet_provisioning_jobs_status;
DROP INDEX IF EXISTS idx_wallet_provisioning_jobs_user_id;
DROP TABLE IF EXISTS wallet_provisioning_jobs;

DROP INDEX IF EXISTS idx_managed_wallets_circle_id;
DROP INDEX IF EXISTS idx_managed_wallets_status;
DROP INDEX IF EXISTS idx_managed_wallets_chain;
DROP INDEX IF EXISTS idx_managed_wallets_wallet_set_id;
DROP INDEX IF EXISTS idx_managed_wallets_user_id;
DROP TABLE IF EXISTS managed_wallets;

DROP INDEX IF EXISTS idx_wallet_sets_circle_id;
DROP INDEX IF EXISTS idx_wallet_sets_status;
DROP TABLE IF EXISTS wallet_sets;
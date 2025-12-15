-- Drop triggers first
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
DROP TRIGGER IF EXISTS update_onboarding_flows_updated_at ON onboarding_flows;
DROP TRIGGER IF EXISTS update_kyc_submissions_updated_at ON kyc_submissions;
DROP TRIGGER IF EXISTS update_wallet_sets_updated_at ON wallet_sets;
DROP TRIGGER IF EXISTS update_managed_wallets_updated_at ON managed_wallets;
DROP TRIGGER IF EXISTS update_wallet_provisioning_jobs_updated_at ON wallet_provisioning_jobs;

-- Drop trigger function
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop tables in reverse order of dependencies
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS wallet_provisioning_jobs;
DROP TABLE IF EXISTS managed_wallets;
DROP TABLE IF EXISTS wallet_sets;
DROP TABLE IF EXISTS kyc_submissions;
DROP TABLE IF EXISTS onboarding_flows;
DROP TABLE IF EXISTS users;

-- Drop extension (be careful with this in production)
-- DROP EXTENSION IF EXISTS "uuid-ossp";
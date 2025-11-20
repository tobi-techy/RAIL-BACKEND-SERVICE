DROP TABLE IF EXISTS tax_reports;
DROP TABLE IF EXISTS portfolio_rebalances;
DROP TABLE IF EXISTS data_privacy_requests;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS aml_checks;
-- Note: kyc_documents and kyc_submissions existed before this migration, not dropping
DROP TABLE IF EXISTS user_preferences;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS fraud_alerts;
DROP TABLE IF EXISTS transaction_limits;

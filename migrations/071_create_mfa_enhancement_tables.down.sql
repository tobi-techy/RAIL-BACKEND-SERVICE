DROP TABLE IF EXISTS incident_response_log;
DROP TABLE IF EXISTS security_incidents;
DROP TABLE IF EXISTS user_behavior_patterns;
DROP TABLE IF EXISTS fraud_signals;
DROP TABLE IF EXISTS blocked_countries;
DROP TABLE IF EXISTS geo_locations;
DROP TABLE IF EXISTS user_mfa_settings;
DROP TABLE IF EXISTS sms_mfa_codes;

DROP FUNCTION IF EXISTS cleanup_old_fraud_signals();

ALTER TABLE users DROP COLUMN IF EXISTS mfa_enforced;
ALTER TABLE users DROP COLUMN IF EXISTS account_value_tier;
ALTER TABLE users DROP COLUMN IF EXISTS fraud_score;
ALTER TABLE users DROP COLUMN IF EXISTS last_fraud_check;

DROP TABLE IF EXISTS fund_through_detections;
DROP TABLE IF EXISTS sanctions_checks;
DROP TABLE IF EXISTS account_freezes;
DROP TABLE IF EXISTS fraud_alerts;
DROP TABLE IF EXISTS fraud_rules;

ALTER TABLE users DROP COLUMN IF EXISTS deposits_frozen;
ALTER TABLE users DROP COLUMN IF EXISTS account_frozen_at;
ALTER TABLE users DROP COLUMN IF EXISTS sanctions_status;

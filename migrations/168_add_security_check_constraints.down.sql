ALTER TABLE whitelisted_addresses DROP CONSTRAINT IF EXISTS valid_status;
ALTER TABLE session_anomalies DROP CONSTRAINT IF EXISTS valid_severity;

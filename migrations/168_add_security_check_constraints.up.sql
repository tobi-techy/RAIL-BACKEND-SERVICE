ALTER TABLE whitelisted_addresses ADD CONSTRAINT valid_status CHECK (status IN ('pending', 'active', 'removed'));
ALTER TABLE whitelisted_addresses ADD CONSTRAINT valid_risk_level CHECK (risk_level IN ('low', 'medium', 'high', 'critical'));
ALTER TABLE session_anomalies ADD CONSTRAINT valid_severity CHECK (severity IN ('low', 'medium', 'high', 'critical'));

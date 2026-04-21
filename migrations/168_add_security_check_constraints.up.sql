ALTER TABLE whitelisted_addresses ADD CONSTRAINT valid_status CHECK (status IN ('pending', 'active', 'removed'));
ALTER TABLE session_anomalies ADD CONSTRAINT valid_severity CHECK (severity IN ('low', 'medium', 'high', 'critical'));

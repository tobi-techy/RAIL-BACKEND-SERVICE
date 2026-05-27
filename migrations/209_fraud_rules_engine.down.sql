-- Safety checks before dropping fraud detection tables
DO $$
BEGIN
    -- Check for active fraud alerts
    IF EXISTS (SELECT 1 FROM fraud_alerts WHERE status IN ('open', 'investigating') LIMIT 1) THEN
        RAISE EXCEPTION 'Cannot drop fraud tables: active fraud alerts exist (status open/investigating). Resolve them first.';
    END IF;

    -- Check for active account freezes
    IF EXISTS (SELECT 1 FROM account_freezes WHERE is_active = true LIMIT 1) THEN
        RAISE EXCEPTION 'Cannot drop fraud tables: active account freezes exist. Unfreeze accounts first.';
    END IF;

    -- Warn about recent sanctions checks
    IF EXISTS (SELECT 1 FROM sanctions_checks WHERE created_at > NOW() - INTERVAL '90 days' LIMIT 1) THEN
        RAISE NOTICE 'WARNING: sanctions_checks contains records from the last 90 days. Ensure compliance audit trail is archived.';
    END IF;
END $$;

DROP TABLE IF EXISTS fund_through_detections;
DROP TABLE IF EXISTS sanctions_checks;
DROP TABLE IF EXISTS account_freezes;
DROP TABLE IF EXISTS fraud_alerts;
DROP TABLE IF EXISTS fraud_rules;

ALTER TABLE users DROP COLUMN IF EXISTS deposits_frozen;
ALTER TABLE users DROP COLUMN IF EXISTS account_frozen_at;
ALTER TABLE users DROP COLUMN IF EXISTS sanctions_status;

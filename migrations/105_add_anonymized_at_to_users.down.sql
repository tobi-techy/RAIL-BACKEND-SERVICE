-- Safety check: prevent dropping anonymized_at if any users have been anonymized
-- This protects GDPR compliance audit trail data from accidental destruction
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE anonymized_at IS NOT NULL LIMIT 1) THEN
        RAISE EXCEPTION 'Cannot drop anonymized_at: anonymized users exist. Archive anonymization data before rolling back.';
    END IF;
END $$;

ALTER TABLE users DROP COLUMN IF EXISTS anonymized_at;

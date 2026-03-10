-- Safety check: refuse rollback if any events have NULL order_id.
-- Investigate and resolve these events before rolling back.
DO $$
DECLARE
    null_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO null_count FROM auto_invest_events WHERE order_id IS NULL;
    IF null_count > 0 THEN
        RAISE EXCEPTION 'Cannot rollback: % auto_invest_events record(s) have NULL order_id. Complete or delete these events before rolling back.', null_count;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_auto_invest_events_correlation_id;
ALTER TABLE auto_invest_events DROP COLUMN IF EXISTS correlation_id;
ALTER TABLE auto_invest_events ALTER COLUMN order_id SET NOT NULL;

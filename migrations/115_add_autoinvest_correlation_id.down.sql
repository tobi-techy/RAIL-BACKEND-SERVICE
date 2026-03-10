-- Handle any events with NULL order_id before adding NOT NULL constraint
DELETE FROM auto_invest_events WHERE order_id IS NULL;

DROP INDEX IF EXISTS idx_auto_invest_events_correlation_id;
ALTER TABLE auto_invest_events DROP COLUMN IF EXISTS correlation_id;
ALTER TABLE auto_invest_events ALTER COLUMN order_id SET NOT NULL;

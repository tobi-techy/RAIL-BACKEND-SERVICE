-- Add correlation_id. Existing rows are backfilled from their id (deterministic, no race).
-- The application always provides correlation_id before inserting new events.
ALTER TABLE auto_invest_events
    ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);

UPDATE auto_invest_events SET correlation_id = id::text WHERE correlation_id IS NULL;

ALTER TABLE auto_invest_events ALTER COLUMN correlation_id SET NOT NULL;

-- Make order_id nullable (orders may not exist yet when event is created)
ALTER TABLE auto_invest_events ALTER COLUMN order_id DROP NOT NULL;

-- Unique index on correlation_id to prevent double investment
CREATE UNIQUE INDEX IF NOT EXISTS idx_auto_invest_events_correlation_id
    ON auto_invest_events(correlation_id);

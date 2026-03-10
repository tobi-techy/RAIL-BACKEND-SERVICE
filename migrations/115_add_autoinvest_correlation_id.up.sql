-- Add correlation_id to auto_invest_events for idempotency tracking
ALTER TABLE auto_invest_events ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);

-- Make order_id nullable (orders may not exist yet when event is created)
ALTER TABLE auto_invest_events ALTER COLUMN order_id DROP NOT NULL;

-- Unique index on correlation_id to prevent double investment
CREATE UNIQUE INDEX IF NOT EXISTS idx_auto_invest_events_correlation_id
    ON auto_invest_events(correlation_id) WHERE correlation_id IS NOT NULL;

-- Prevent duplicate deliveries for the same user+campaign on the same day.
-- This handles the race condition when multiple ECS tasks run the growth worker simultaneously.
-- We add a dedup_key column that stores user_id+campaign_id+date for conflict detection.
ALTER TABLE campaign_deliveries ADD COLUMN IF NOT EXISTS dedup_key TEXT;

-- Backfill existing rows
UPDATE campaign_deliveries SET dedup_key = user_id::text || ':' || campaign_id::text || ':' || created_at::date::text
WHERE dedup_key IS NULL;

-- Create unique index on dedup_key for queued/sent deliveries
CREATE UNIQUE INDEX IF NOT EXISTS idx_campaign_deliveries_dedup
    ON campaign_deliveries (dedup_key)
    WHERE status IN ('queued', 'sent');

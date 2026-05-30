DROP INDEX IF EXISTS idx_campaign_deliveries_dedup;
ALTER TABLE campaign_deliveries DROP COLUMN IF EXISTS dedup_key;

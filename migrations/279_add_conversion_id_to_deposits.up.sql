-- Add conversion_id column to deposits for Graph conversion traceability.
-- This enables targeted matching when Graph fires conversion.failed webhooks.
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS conversion_id VARCHAR(255) DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_deposits_conversion_id ON deposits (conversion_id) WHERE conversion_id IS NOT NULL;

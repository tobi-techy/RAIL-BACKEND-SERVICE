-- Add idempotency_key and correlation_id columns to deposits table for proper deposit deduplication
-- This is required for the multichain deposit flow to work correctly

ALTER TABLE deposits ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255) UNIQUE;
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);

-- Create index on idempotency_key for fast lookups
CREATE INDEX IF NOT EXISTS idx_deposits_idempotency_key ON deposits(idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Create index on correlation_id for tracing
CREATE INDEX IF NOT EXISTS idx_deposits_correlation_id ON deposits(correlation_id) WHERE correlation_id IS NOT NULL;

-- Add idempotency_key and correlation_id columns to deposits table for proper deposit deduplication
-- This is required for the multichain deposit flow to work correctly

-- Step 1: Add idempotency_key column as nullable first (if not exists)
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);

-- Step 2: Backfill existing rows with generated idempotency keys using COALESCE to handle NULLs
UPDATE deposits 
SET idempotency_key = CONCAT(
    COALESCE(tx_hash, id::text),
    '-',
    COALESCE(chain, 'unknown'),
    '-',
    user_id::text,
    '-',
    COALESCE(created_at::text, 'unknown')
)
WHERE idempotency_key IS NULL;

-- Step 3: Add NOT NULL constraint (requires all existing rows to have values)
ALTER TABLE deposits ALTER COLUMN idempotency_key SET NOT NULL;

-- Step 4: Add UNIQUE constraint for idempotency
ALTER TABLE deposits ADD CONSTRAINT deposits_idempotency_key_unique UNIQUE (idempotency_key);

-- Create index on idempotency_key for fast lookups
CREATE INDEX IF NOT EXISTS idx_deposits_idempotency_key ON deposits(idempotency_key);

-- Create index on correlation_id for tracing
CREATE INDEX IF NOT EXISTS idx_deposits_correlation_id ON deposits(correlation_id);

-- Add idempotency_key column to p2p_transfers for preventing duplicate transfers
ALTER TABLE p2p_transfers ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(256);

-- Set existing NULL values to unique placeholder values to avoid unique constraint violation
UPDATE p2p_transfers SET idempotency_key = 'legacy-' || id::text WHERE idempotency_key IS NULL;

-- Add NOT NULL constraint
ALTER TABLE p2p_transfers ALTER COLUMN idempotency_key SET NOT NULL;

-- Add unique index on idempotency_key for fast lookups and idempotency enforcement
CREATE UNIQUE INDEX IF NOT EXISTS idx_p2p_transfers_idempotency_key ON p2p_transfers (idempotency_key) WHERE idempotency_key != '';

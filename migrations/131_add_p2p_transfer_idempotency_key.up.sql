-- Add idempotency_key column to p2p_transfers for preventing duplicate transfers
ALTER TABLE p2p_transfers ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(256);

-- Add unique index on idempotency_key for fast lookups and idempotency enforcement
CREATE UNIQUE INDEX IF NOT EXISTS idx_p2p_transfers_idempotency_key ON p2p_transfers (idempotency_key) WHERE idempotency_key IS NOT NULL;

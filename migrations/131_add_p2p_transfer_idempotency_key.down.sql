-- Rollback: remove idempotency_key column from p2p_transfers
DROP INDEX IF EXISTS idx_p2p_transfers_idempotency_key;
ALTER TABLE p2p_transfers DROP COLUMN IF EXISTS idempotency_key;

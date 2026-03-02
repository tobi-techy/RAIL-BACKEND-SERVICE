-- Rollback: Remove idempotency_key and correlation_id columns from deposits table

DROP INDEX IF EXISTS idx_deposits_idempotency_key;
DROP INDEX IF EXISTS idx_deposits_correlation_id;

ALTER TABLE deposits DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE deposits DROP COLUMN IF EXISTS correlation_id;

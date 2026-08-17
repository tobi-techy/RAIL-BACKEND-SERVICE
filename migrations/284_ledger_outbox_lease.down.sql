DROP INDEX IF EXISTS idx_ledger_outbox_claimable;

ALTER TABLE ledger_outbox DROP COLUMN IF EXISTS claimed_at;

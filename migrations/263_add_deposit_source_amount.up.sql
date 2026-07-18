-- Persist the original source amount/currency of a deposit (e.g. the raw NGN
-- amount before NGN→USDC conversion). Enables the recovery worker to re-drive a
-- deposit whose conversion or ledger credit failed after the idempotency row was
-- claimed, and gives reconciliation an audit trail of realized-vs-source value.
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS source_amount NUMERIC(36,18);
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS source_currency VARCHAR(8);

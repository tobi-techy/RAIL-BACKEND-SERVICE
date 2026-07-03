-- Exact USDC amount (incl. ChainRails fees) to fund a sweep's bridge intent
-- with, persisted at intent creation. Retried funding attempts MUST reuse this
-- exact figure with the same per-intent Circle idempotency key, so a retry can
-- never issue a second, differently-sized transfer to the same intent.
ALTER TABLE deposit_sweeps ADD COLUMN IF NOT EXISTS funding_amount NUMERIC(20, 6);

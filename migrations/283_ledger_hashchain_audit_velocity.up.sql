-- 283: Ledger hash chain, audit trail, and velocity limits
--
-- Layer 1: Tamper-evident hash chain
--   Each transaction stores previous_transaction_hash and its own
--   transaction_hash = SHA256(prev_hash + normalized fields).
--   Any retroactive edit to an old transaction breaks the chain.
--
-- Layer 2: Audit trail (initiated_by, reason)
--   Every transaction tracks who/what initiated it and why.
--
-- Layer 3: Per-account daily velocity buckets
--   Tracks cumulative outflow and transaction count per day so the
--   ledger service can enforce velocity limits (circuit breaker).

-- Layer 1: Hash chain columns
ALTER TABLE ledger_transactions
    ADD COLUMN IF NOT EXISTS previous_transaction_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS transaction_hash TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN ledger_transactions.previous_transaction_hash IS 'SHA256 hash of the chronologically prior transaction (empty for genesis)';
COMMENT ON COLUMN ledger_transactions.transaction_hash IS 'SHA256 hash of this transaction for tamper-evident chaining';

CREATE INDEX IF NOT EXISTS idx_ledger_transactions_hash ON ledger_transactions (transaction_hash);

-- Layer 2: Audit trail
ALTER TABLE ledger_transactions
    ADD COLUMN IF NOT EXISTS initiated_by TEXT NOT NULL DEFAULT 'system',
    ADD COLUMN IF NOT EXISTS reason TEXT;

COMMENT ON COLUMN ledger_transactions.initiated_by IS 'Who/what created this transaction: user, system, admin, webhook, bridge, automation';
COMMENT ON COLUMN ledger_transactions.reason IS 'Human-readable explanation of why this transaction was created';

-- Layer 3: Velocity buckets
CREATE TABLE IF NOT EXISTS ledger_velocity_buckets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID NOT NULL REFERENCES ledger_accounts(id),
    bucket_date     DATE NOT NULL,
    outflow_total   NUMERIC(40, 10) NOT NULL DEFAULT 0,
    tx_count        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, bucket_date)
);

CREATE INDEX IF NOT EXISTS idx_ledger_velocity_buckets_lookup
    ON ledger_velocity_buckets (account_id, bucket_date);

COMMENT ON TABLE ledger_velocity_buckets IS 'Daily velocity tracking per account for circuit-breaker limits';
COMMENT ON COLUMN ledger_velocity_buckets.outflow_total IS 'Cumulative debit amount for this account on this date';
COMMENT ON COLUMN ledger_velocity_buckets.tx_count IS 'Number of debit transactions for this account on this date';

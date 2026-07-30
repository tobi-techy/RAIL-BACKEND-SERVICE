CREATE TABLE IF NOT EXISTS ledger_outbox (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      TEXT NOT NULL,
    aggregate_id    UUID NOT NULL,
    aggregate_type  TEXT NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

CREATE INDEX idx_ledger_outbox_unpublished
    ON ledger_outbox (created_at)
    WHERE published_at IS NULL;

CREATE INDEX idx_ledger_outbox_stale
    ON ledger_outbox (retry_count DESC, created_at)
    WHERE published_at IS NULL;

COMMENT ON TABLE ledger_outbox IS 'Transactional outbox for ledger events. Events are written in the same DB transaction as the ledger write, ensuring at-least-once delivery to downstream consumers.';
COMMENT ON COLUMN ledger_outbox.event_type IS 'e.g. transaction.created, transaction.completed, transaction.reversed, balance.updated';
COMMENT ON COLUMN ledger_outbox.aggregate_id IS 'ID of the aggregate (transaction ID or account ID)';
COMMENT ON COLUMN ledger_outbox.aggregate_type IS 'e.g. ledger_transaction, ledger_account';
COMMENT ON COLUMN ledger_outbox.retry_count IS 'Number of failed publish attempts; dead-letter when exceeding threshold';
COMMENT ON COLUMN ledger_outbox.last_error IS 'Last error message from a failed publish attempt';

CREATE TABLE IF NOT EXISTS ledger_balance_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID NOT NULL REFERENCES ledger_accounts(id),
    balance         NUMERIC(40, 10) NOT NULL,
    snapshot_date   DATE NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, snapshot_date)
);

CREATE INDEX idx_ledger_balance_snapshots_date
    ON ledger_balance_snapshots (snapshot_date DESC);

COMMENT ON TABLE ledger_balance_snapshots IS 'Daily balance snapshots for point-in-time queries and reconciliation.';

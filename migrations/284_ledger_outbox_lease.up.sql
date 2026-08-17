-- Lease-based outbox claiming. A claim reserves an event (claimed_at) for
-- dispatch instead of marking it published, so a crash between claim and
-- publish no longer silently drops the event: the lease expires and the event
-- is reclaimed on a later tick. published_at is only set once dispatch succeeds.
ALTER TABLE ledger_outbox ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;

-- Claim index narrowed to claimable rows: dead-lettered events (retry_count at
-- the ceiling) are excluded so the 5s claim query stays cheap as dead letters
-- accumulate pending retention cleanup.
CREATE INDEX IF NOT EXISTS idx_ledger_outbox_claimable
    ON ledger_outbox (created_at)
    WHERE published_at IS NULL AND retry_count < 10;

COMMENT ON COLUMN ledger_outbox.claimed_at IS 'Lease timestamp set when a publisher claims the event for dispatch. Cleared once published_at is set or the event is retried; expired leases are reclaimed by a later tick.';

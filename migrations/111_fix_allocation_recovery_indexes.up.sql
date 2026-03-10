-- Index for deposit allocation recovery worker JOIN: ae.source_tx_id = d.tx_hash
-- Without this, the LEFT JOIN does a full scan of allocation_events per deposit candidate.
CREATE INDEX IF NOT EXISTS idx_allocation_events_source_tx_id ON allocation_events(source_tx_id)
    WHERE source_tx_id IS NOT NULL;

-- Expand deposits status constraint to include statuses used in application code.
-- 'expired' and 'timeout' are in ValidDepositStatuses (Go) but were missing from the DB check.
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'pending',
        'confirmed',
        'failed',
        'expired',
        'timeout',
        'off_ramp_initiated',
        'off_ramp_completed',
        'broker_funded'
    ));

DROP INDEX IF EXISTS idx_allocation_events_source_tx_id;

ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'pending',
        'confirmed',
        'failed',
        'off_ramp_initiated',
        'off_ramp_completed',
        'broker_funded'
    ));

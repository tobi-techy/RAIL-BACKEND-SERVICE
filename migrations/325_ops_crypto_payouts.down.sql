-- Reverting this migration is only safe once no 'bridged' deposit rows exist:
-- the narrower status constraint would reject them, which is intentional (a
-- rollback must not silently forget that money was paid out off-ledger).
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'initiated', 'pending', 'confirmed', 'failed', 'expired', 'timeout',
        'off_ramp_initiated', 'off_ramp_completed', 'broker_funded'
    ));

DROP TABLE IF EXISTS ops_crypto_payouts;

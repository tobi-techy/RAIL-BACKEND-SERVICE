-- Tracks that a parked PAJ offramp order has already been escalated for manual
-- review, so the recovery worker logs it once instead of on every 2-minute pass.
--
-- Why a column and not just "don't log": the worker's escalation path exists to
-- surface orders with funds possibly in flight that no automated path may
-- reverse (see paj_offramp_recovery.flagStartedButStuck). It had no memory, so
-- each stuck order produced an ERROR line every cycle for as long as it stayed
-- stuck — on 2026-10-08 that was ~18 orders re-logged every 2 minutes forever.
-- Recording the escalation keeps the signal (once) and makes the parked set
-- queryable: SELECT ... WHERE manual_review_flagged_at IS NOT NULL.
ALTER TABLE paj_orders
    ADD COLUMN IF NOT EXISTS manual_review_flagged_at TIMESTAMPTZ;

-- Index support for the sweep double-credit detector (blend worker
-- detectSweepDoubleCredits, runs every reconcile tick): joins recent sweeps to
-- deposits on user_id + created_at range (amount equality is applied as a
-- filter after the index narrows the candidate rows), and filters sweeps by
-- swept_at / completed_at recency.
--
-- Plain CREATE INDEX (not CONCURRENTLY) on purpose: the migration runner
-- executes each file as one batched statement (an implicit transaction block),
-- where CONCURRENTLY is rejected by Postgres. These tables are small, so the
-- build lock is momentary.
CREATE INDEX IF NOT EXISTS idx_deposits_user_created
    ON deposits (user_id, created_at);

CREATE INDEX IF NOT EXISTS idx_blend_redemptions_swept_at
    ON blend_yield_redemptions (swept_at)
    WHERE swept_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_deposit_sweeps_completed_at
    ON deposit_sweeps (completed_at)
    WHERE status = 'completed';

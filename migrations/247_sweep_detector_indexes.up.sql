-- Index support for the sweep double-credit detector (blend worker
-- detectSweepDoubleCredits, runs every reconcile tick): joins recent sweeps to
-- deposits on (user_id, amount, created_at range), and filters sweeps by
-- swept_at / completed_at recency.
CREATE INDEX IF NOT EXISTS idx_deposits_user_amount_created
    ON deposits (user_id, amount, created_at);

CREATE INDEX IF NOT EXISTS idx_blend_redemptions_swept_at
    ON blend_yield_redemptions (swept_at)
    WHERE swept_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_deposit_sweeps_completed_at
    ON deposit_sweeps (completed_at)
    WHERE status = 'completed';

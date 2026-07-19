-- Miriam self-review: periodic audit where Miriam grades her own recent actions
-- and messaging, then records the verdict and any behavioral adjustments she made.
-- This closes the loop on her own work (not just her predictions), keeping her
-- accountable and letting harmful/ignored patterns self-suppress over time.
CREATE TABLE IF NOT EXISTS miriam_self_reviews (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    actions_reviewed INTEGER NOT NULL DEFAULT 0,
    actions_helped INTEGER NOT NULL DEFAULT 0,
    actions_neutral INTEGER NOT NULL DEFAULT 0,
    actions_harmed INTEGER NOT NULL DEFAULT 0,
    nudges_sent INTEGER NOT NULL DEFAULT 0,
    nudges_dismissed INTEGER NOT NULL DEFAULT 0,
    avg_health_before INTEGER NOT NULL DEFAULT 0,
    avg_health_after INTEGER NOT NULL DEFAULT 0,
    cadence_hint TEXT NOT NULL DEFAULT 'normal' CHECK (cadence_hint IN ('normal', 'reduce')),
    verdict TEXT NOT NULL DEFAULT '',
    adjustments JSONB NOT NULL DEFAULT '{}'::jsonb,
    note_sent BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_self_reviews_user_recent
    ON miriam_self_reviews(user_id, created_at DESC);

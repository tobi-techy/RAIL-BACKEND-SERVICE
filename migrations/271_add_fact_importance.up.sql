-- Add importance score to miriam_user_facts for memory quality gating.
-- Importance: 0 (trivial) to 10 (critical). Only facts with importance >= 5 are persisted.
ALTER TABLE miriam_user_facts
    ADD COLUMN IF NOT EXISTS importance SMALLINT NOT NULL DEFAULT 5;

-- Index for ranked retrieval: importance + recency composite.
CREATE INDEX IF NOT EXISTS idx_miriam_user_facts_importance_recency
    ON miriam_user_facts (user_id, importance DESC, last_confirmed_at DESC)
    WHERE superseded_by IS NULL;

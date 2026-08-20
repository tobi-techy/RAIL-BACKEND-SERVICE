-- user_goals + user_goal_progress_events
-- Free-standing savings goals (separate from goal-linked automations which use
-- the existing `goals` table). The new table is the persistence layer for
-- Miriam's Baby-Steps-driven goal coaching and milestone notifications.
--
-- A goal can be tagged with a Baby Step (1-7) so the goal_progress worker can
-- gate step-advance events on completion of every goal on the current step.
-- Free-standing goals (no step) live alongside the ladder without affecting it.

CREATE TABLE user_goals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    target_amount NUMERIC(20,4) NOT NULL CHECK (target_amount > 0),
    target_currency TEXT NOT NULL DEFAULT 'USD',
    current_amount NUMERIC(20,4) NOT NULL DEFAULT 0 CHECK (current_amount >= 0),
    deadline TIMESTAMPTZ,
    baby_step SMALLINT CHECK (baby_step IS NULL OR (baby_step BETWEEN 1 AND 7)),
    category TEXT NOT NULL DEFAULT 'freeform',
    source TEXT NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    archived_at TIMESTAMPTZ
);

-- Active-goal index: covers the hot read path (list active goals for a user)
-- and the goal_progress worker's "current step's goals" lookup.
CREATE INDEX user_goals_user_active_idx
    ON user_goals(user_id) WHERE completed_at IS NULL AND archived_at IS NULL;

-- Step-gated lookup: "find active goals on the user's current Baby Step".
CREATE INDEX user_goals_user_step_idx
    ON user_goals(user_id, baby_step) WHERE completed_at IS NULL;

-- Append-only event log for milestone / pace / step-advance auditability.
CREATE TABLE user_goal_progress_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    goal_id UUID NOT NULL REFERENCES user_goals(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    pct NUMERIC(5,2),
    current_amount NUMERIC(20,4),
    target_amount NUMERIC(20,4),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Last-N events per goal for chat history and audit.
CREATE INDEX user_goal_progress_events_goal_idx
    ON user_goal_progress_events(goal_id, created_at DESC);

-- User-scoped event timeline (for "what happened on my goals today?" queries).
CREATE INDEX user_goal_progress_events_user_idx
    ON user_goal_progress_events(user_id, created_at DESC);

COMMENT ON TABLE user_goals IS 'Free-standing savings goals surfaced by Miriam. Tag with baby_step 1-7 to participate in step-advance flow; leave NULL for freeform goals.';
COMMENT ON TABLE user_goal_progress_events IS 'Append-only audit trail for goal-progress events: milestones, pace reports, step advances.';

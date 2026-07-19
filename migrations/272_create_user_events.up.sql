-- Financial event timeline: structured events Miriam can query for context.
-- More useful than chat logs — captures salary received, goal completed, budget exceeded, etc.
CREATE TABLE IF NOT EXISTS miriam_user_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(50) NOT NULL,
    title TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_user_events_user_time
    ON miriam_user_events (user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_miriam_user_events_user_type
    ON miriam_user_events (user_id, event_type, occurred_at DESC);

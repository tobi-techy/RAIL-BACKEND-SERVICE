CREATE TABLE IF NOT EXISTS miriam_mandate_suggestions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    action_type TEXT NOT NULL,
    reasoning TEXT NOT NULL DEFAULT '',
    suggested_max_amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    suggested_max_day NUMERIC(20, 8) NOT NULL DEFAULT 0,
    suggested_min_balance NUMERIC(20, 8) NOT NULL DEFAULT 0,
    suggested_cooldown INTEGER NOT NULL DEFAULT 1440,
    confidence INTEGER NOT NULL DEFAULT 50 CHECK (confidence BETWEEN 0 AND 100),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'dismissed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dismissed_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_suggestions_user_pending
    ON miriam_mandate_suggestions(user_id, status)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_suggestions_user_recent
    ON miriam_mandate_suggestions(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS miriam_notification_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    preferences JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS miriam_notification_digests (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generated_at TIMESTAMPTZ NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_digests_user_recent
    ON miriam_notification_digests(user_id, generated_at DESC);

CREATE TABLE IF NOT EXISTS money_guard_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    guardian_mode VARCHAR(20) NOT NULL DEFAULT 'nudge'
        CHECK (guardian_mode IN ('observe', 'nudge', 'strict')),
    decimal_sweep_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    stash_raid_limit_per_month INTEGER NOT NULL DEFAULT 2
        CHECK (stash_raid_limit_per_month BETWEEN 0 AND 20),
    card_cooldown_minutes INTEGER NOT NULL DEFAULT 30
        CHECK (card_cooldown_minutes BETWEEN 0 AND 1440),
    safe_to_spend_floor NUMERIC(20, 2) NOT NULL DEFAULT 10
        CHECK (safe_to_spend_floor >= 0),
    recovery_mode_until TIMESTAMPTZ,
    last_weekly_autopsy_sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS spending_caps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope VARCHAR(32) NOT NULL CHECK (scope IN ('daily', 'weekly', 'monthly', 'category', 'merchant', 'withdrawal')),
    scope_value TEXT NOT NULL DEFAULT '',
    period VARCHAR(16) NOT NULL DEFAULT 'month' CHECK (period IN ('day', 'week', 'month')),
    limit_amount NUMERIC(20, 2) NOT NULL CHECK (limit_amount > 0),
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    enforcement_action VARCHAR(32) NOT NULL DEFAULT 'warn'
        CHECK (enforcement_action IN ('warn', 'require_passcode', 'pause_card', 'decline')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_spending_caps_user_active
    ON spending_caps(user_id, is_active, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_spending_caps_user_scope
    ON spending_caps(user_id, scope) WHERE is_active = TRUE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_spending_caps_user_scope_unique
    ON spending_caps(user_id, scope, scope_value, period) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS money_guard_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    severity VARCHAR(16) NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    amount NUMERIC(20, 2) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    merchant TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    action VARCHAR(32) NOT NULL DEFAULT 'warn',
    message TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_money_guard_events_user_recent
    ON money_guard_events(user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_money_guard_events_user_severity
    ON money_guard_events(user_id, severity, occurred_at DESC);

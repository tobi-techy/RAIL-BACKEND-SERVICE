CREATE TABLE IF NOT EXISTS miriam_money_states (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    income_cadence TEXT NOT NULL DEFAULT 'unknown',
    avg_monthly_income NUMERIC(20, 8) NOT NULL DEFAULT 0,
    upcoming_obligations NUMERIC(20, 8) NOT NULL DEFAULT 0,
    safe_to_spend_daily NUMERIC(20, 8) NOT NULL DEFAULT 0,
    liquidity_runway_days INTEGER NOT NULL DEFAULT 0,
    stash_target NUMERIC(20, 8) NOT NULL DEFAULT 0,
    recurring_spend_monthly NUMERIC(20, 8) NOT NULL DEFAULT 0,
    anomaly_count INTEGER NOT NULL DEFAULT 0,
    confidence_level TEXT NOT NULL DEFAULT 'low',
    confidence_score INTEGER NOT NULL DEFAULT 0,
    last_evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_money_states_evaluated
    ON miriam_money_states(last_evaluated_at);

CREATE TABLE IF NOT EXISTS miriam_autopilot_mandates (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    action_type TEXT NOT NULL CHECK (action_type IN ('transfer_to_stash')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'expired')),
    max_amount_per_action NUMERIC(20, 8) NOT NULL,
    max_amount_per_day NUMERIC(20, 8) NOT NULL,
    min_spend_balance NUMERIC(20, 8) NOT NULL DEFAULT 0,
    min_safe_to_spend NUMERIC(20, 8) NOT NULL DEFAULT 0,
    cooldown_minutes INTEGER NOT NULL DEFAULT 1440,
    last_executed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_mandates_user_status
    ON miriam_autopilot_mandates(user_id, status);

CREATE INDEX IF NOT EXISTS idx_miriam_mandates_active
    ON miriam_autopilot_mandates(status, action_type)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS miriam_decision_receipts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mandate_id UUID REFERENCES miriam_autopilot_mandates(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    action_type TEXT NOT NULL,
    amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    status TEXT NOT NULL CHECK (status IN ('suggested', 'executed', 'skipped', 'failed')),
    reason TEXT NOT NULL,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_receipts_user_recent
    ON miriam_decision_receipts(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_miriam_receipts_mandate_recent
    ON miriam_decision_receipts(mandate_id, created_at DESC)
    WHERE mandate_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS miriam_events (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info',
    amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_events_user_recent
    ON miriam_events(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_miriam_events_type_recent
    ON miriam_events(event_type, created_at DESC);

CREATE TABLE IF NOT EXISTS miriam_learning_signals (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receipt_id UUID NOT NULL REFERENCES miriam_decision_receipts(id) ON DELETE CASCADE,
    signal TEXT NOT NULL CHECK (signal IN ('accepted', 'ignored', 'reversed', 'dismissed')),
    weight NUMERIC(10, 4) NOT NULL DEFAULT 1,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_miriam_learning_user_recent
    ON miriam_learning_signals(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_miriam_learning_receipt
    ON miriam_learning_signals(receipt_id);

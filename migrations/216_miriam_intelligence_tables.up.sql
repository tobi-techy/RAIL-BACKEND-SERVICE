CREATE TABLE IF NOT EXISTS miriam_decisions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mandate_id UUID NOT NULL REFERENCES miriam_autopilot_mandates(id) ON DELETE CASCADE,
    decision_type TEXT NOT NULL CHECK (decision_type IN ('execute', 'delay', 'adjust_amount', 'skip', 'escalate')),
    reason TEXT NOT NULL,
    confidence_score INTEGER NOT NULL DEFAULT 50,
    factors JSONB NOT NULL DEFAULT '{}'::jsonb,
    original_amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    adjusted_amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_decisions_user_recent
    ON miriam_decisions(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_decisions_mandate
    ON miriam_decisions(mandate_id);

CREATE TABLE IF NOT EXISTS miriam_decision_outcomes (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision_id UUID NOT NULL REFERENCES miriam_decisions(id) ON DELETE CASCADE,
    outcome TEXT NOT NULL CHECK (outcome IN ('user_happy', 'user_reversed', 'user_ignored', 'user_dismissed', 'system_error')),
    outcome_delay INTERVAL NOT NULL DEFAULT '0',
    feedback_score NUMERIC(10, 4) NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outcomes_decision
    ON miriam_decision_outcomes(decision_id);

CREATE INDEX IF NOT EXISTS idx_outcomes_user_recent
    ON miriam_decision_outcomes(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS miriam_learning_models (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    total_decisions INTEGER NOT NULL DEFAULT 0,
    positive_outcomes INTEGER NOT NULL DEFAULT 0,
    negative_outcomes INTEGER NOT NULL DEFAULT 0,
    current_bias NUMERIC(10, 4) NOT NULL DEFAULT 0,
    confidence_weight NUMERIC(10, 4) NOT NULL DEFAULT 0,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, category)
);

CREATE TABLE IF NOT EXISTS miriam_predictions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prediction_type TEXT NOT NULL,
    horizon TEXT NOT NULL CHECK (horizon IN ('3_day', '7_day', '14_day', '30_day')),
    probability NUMERIC(5, 4) NOT NULL DEFAULT 0,
    severity TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    projected_amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    reasoning TEXT NOT NULL,
    data_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_predictions_user_active
    ON miriam_predictions(user_id, expires_at);

CREATE INDEX IF NOT EXISTS idx_predictions_type
    ON miriam_predictions(prediction_type, severity);

CREATE TABLE IF NOT EXISTS proactive_nudges (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    trigger_type TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    message TEXT NOT NULL,
    action_suggestion JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    dismissed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nudges_user_pending
    ON proactive_nudges(user_id, delivered_at, dismissed_at)
    WHERE delivered_at IS NULL AND dismissed_at IS NULL;

CREATE TABLE IF NOT EXISTS miriam_financial_health_scores (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    overall_score INTEGER NOT NULL,
    budget_score INTEGER NOT NULL,
    savings_score INTEGER NOT NULL,
    debt_score INTEGER NOT NULL,
    runway_score INTEGER NOT NULL,
    stability_score INTEGER NOT NULL,
    trend TEXT NOT NULL DEFAULT 'stable',
    previous_score INTEGER NOT NULL DEFAULT 0,
    reasoning TEXT NOT NULL,
    data_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_health_user_recent
    ON miriam_financial_health_scores(user_id, created_at DESC);

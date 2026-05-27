CREATE TABLE IF NOT EXISTS miriam_prediction_outcomes (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prediction_id UUID NOT NULL,
    prediction_type TEXT NOT NULL,
    predicted_probability NUMERIC(6,4) NOT NULL,
    horizon_days INTEGER NOT NULL DEFAULT 7,
    threshold_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    actual_outcome BOOLEAN,
    outcome_observed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prediction_outcomes_user
    ON miriam_prediction_outcomes(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_prediction_outcomes_prediction
    ON miriam_prediction_outcomes(prediction_id);

CREATE INDEX IF NOT EXISTS idx_prediction_outcomes_pending
    ON miriam_prediction_outcomes(actual_outcome)
    WHERE actual_outcome IS NULL;

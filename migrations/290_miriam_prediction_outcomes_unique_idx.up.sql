CREATE UNIQUE INDEX IF NOT EXISTS idx_miriam_prediction_outcomes_unique
    ON miriam_prediction_outcomes(user_id, prediction_id, prediction_type);
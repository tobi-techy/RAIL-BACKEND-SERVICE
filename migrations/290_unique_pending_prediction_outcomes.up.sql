-- Add unique constraint to prevent duplicate pending prediction outcomes
-- for the same user and prediction type. Only applies to pending rows
-- (actual_outcome IS NULL) so completed outcomes can coexist.

CREATE UNIQUE INDEX IF NOT EXISTS uq_miriam_prediction_outcomes_user_type_pending
    ON miriam_prediction_outcomes(user_id, prediction_type)
    WHERE actual_outcome IS NULL;

-- Remove unique constraint on pending prediction outcomes
DROP INDEX IF EXISTS uq_miriam_prediction_outcomes_user_type_pending;

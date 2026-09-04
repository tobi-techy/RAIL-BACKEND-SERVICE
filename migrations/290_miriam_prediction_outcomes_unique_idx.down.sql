-- +migrate DownNoTransaction
-- Drop the unique index
DROP INDEX CONCURRENTLY IF EXISTS uq_miriam_prediction_outcomes_user_type_pending;

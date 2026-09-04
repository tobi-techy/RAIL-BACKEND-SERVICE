-- +migrate UpNoTransaction
-- 300: Add unique index to prevent duplicate pending prediction outcomes
-- First, clean up any existing duplicates (keep the earliest created_at per user+type)
WITH duplicates AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY user_id, prediction_type ORDER BY created_at ASC) as rn
    FROM miriam_prediction_outcomes
    WHERE actual_outcome IS NULL
)
DELETE FROM miriam_prediction_outcomes
WHERE id IN (SELECT id FROM duplicates WHERE rn > 1);

-- Add unique constraint to prevent duplicate pending prediction outcomes
-- for the same user and prediction type. Only applies to pending rows
-- (actual_outcome IS NULL) so completed outcomes can coexist.
-- Note: PostgreSQL does not support CREATE UNIQUE INDEX CONCURRENTLY.
-- This migration runs without a transaction (UpNoTransaction) to avoid
-- holding locks during the index build. The table is small so the
-- exclusive lock duration is minimal.
CREATE UNIQUE INDEX IF NOT EXISTS uq_miriam_prediction_outcomes_user_type_pending
ON miriam_prediction_outcomes(user_id, prediction_type)
WHERE actual_outcome IS NULL;

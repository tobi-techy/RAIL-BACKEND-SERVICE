-- Migration 278: Drop legacy 'message' column from notifications table.
-- All application code uses the 'body' column (added in migration 109).
-- Migration 118 tried to recreate the table with 'body' but was a no-op
-- (CREATE TABLE IF NOT EXISTS), leaving the old 'message TEXT NOT NULL' in place.
-- Every INSERT that omits 'message' fails with:
--   pq: null value in column "message" violates not-null constraint
ALTER TABLE notifications DROP COLUMN IF EXISTS message;

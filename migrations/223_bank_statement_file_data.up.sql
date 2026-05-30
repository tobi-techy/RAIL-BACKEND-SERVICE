-- Store PDF file data directly in the DB so uploads survive server restarts
ALTER TABLE bank_statement_uploads ADD COLUMN IF NOT EXISTS file_data BYTEA;

-- Backfill: mark existing null file_data as non-usable (they were temp files that may be gone)
-- These uploads will need to be re-uploaded.

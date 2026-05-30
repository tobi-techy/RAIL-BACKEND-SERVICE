-- Store structured summary JSON for enriched status responses.
-- This is a NEW column added by this migration. It has never been deployed
-- with a TEXT type, so no existing data migration is required.
ALTER TABLE bank_statement_uploads ADD COLUMN IF NOT EXISTS summary JSONB;

-- Store structured summary JSON for enriched status responses
ALTER TABLE bank_statement_uploads ADD COLUMN IF NOT EXISTS summary TEXT;

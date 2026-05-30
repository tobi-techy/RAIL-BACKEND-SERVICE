ALTER TABLE bank_statement_uploads DROP COLUMN IF EXISTS summary;
-- Note: if summary contained JSONB data, casting back to TEXT during a future
-- column rename would be: ALTER TABLE ... ALTER COLUMN summary TYPE TEXT USING summary::text;

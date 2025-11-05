-- Rollback enhanced audit logs changes

-- Drop view
DROP VIEW IF EXISTS audit_summary;

-- Drop constraint
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS chk_audit_financial_consistency;

-- Drop indexes
DROP INDEX IF EXISTS idx_audit_logs_amount;
DROP INDEX IF EXISTS idx_audit_logs_currency;
DROP INDEX IF EXISTS idx_audit_logs_signature;
DROP INDEX IF EXISTS idx_audit_logs_resource_id;
DROP INDEX IF EXISTS idx_audit_logs_user_created;

-- Drop columns
ALTER TABLE audit_logs 
DROP COLUMN IF EXISTS amount,
DROP COLUMN IF EXISTS currency,
DROP COLUMN IF EXISTS signature,
DROP COLUMN IF EXISTS resource_id;

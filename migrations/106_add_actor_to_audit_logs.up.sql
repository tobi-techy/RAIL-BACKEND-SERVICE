-- Add actor column to audit_logs table
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS actor VARCHAR(100);

CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs(actor) WHERE actor IS NOT NULL;

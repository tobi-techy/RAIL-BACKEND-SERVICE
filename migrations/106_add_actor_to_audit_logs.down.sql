-- Remove actor column from audit_logs table
ALTER TABLE audit_logs DROP COLUMN IF EXISTS actor;

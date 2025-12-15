-- Add user_agent column to audit_logs table
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS user_agent TEXT;
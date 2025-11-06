-- Enhanced audit logs with financial tracking and integrity verification
-- Add new columns to existing audit_logs table

ALTER TABLE audit_logs 
ADD COLUMN IF NOT EXISTS amount DECIMAL(36, 18),
ADD COLUMN IF NOT EXISTS currency VARCHAR(10),
ADD COLUMN IF NOT EXISTS signature VARCHAR(128) NOT NULL DEFAULT '',
ADD COLUMN IF NOT EXISTS resource_id VARCHAR(100);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_audit_logs_amount ON audit_logs(amount) WHERE amount IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_logs_currency ON audit_logs(currency) WHERE currency IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_logs_signature ON audit_logs(signature);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_id ON audit_logs(resource_id) WHERE resource_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_at ON audit_logs(user_id, at DESC);

-- Add constraint to ensure financial transactions have currency when amount is present
DO $$ 
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_audit_financial_consistency') THEN
        ALTER TABLE audit_logs 
        ADD CONSTRAINT chk_audit_financial_consistency 
        CHECK ((amount IS NULL AND currency IS NULL) OR (amount IS NOT NULL AND currency IS NOT NULL));
    END IF;
END $$;

-- Create audit summary view for reporting
CREATE OR REPLACE VIEW audit_summary AS
SELECT 
    DATE_TRUNC('day', at) as audit_date,
    user_id,
    action,
    resource_type,
    COUNT(*) as event_count,
    COUNT(CASE WHEN status = 'success' THEN 1 END) as success_count,
    COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed_count,
    SUM(CASE WHEN amount IS NOT NULL THEN amount ELSE 0 END) as total_amount
FROM audit_logs
GROUP BY DATE_TRUNC('day', at), user_id, action, resource_type
ORDER BY audit_date DESC;

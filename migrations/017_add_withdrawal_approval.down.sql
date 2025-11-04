-- Remove withdrawal approval system

-- Drop RLS policies
DROP POLICY IF EXISTS withdrawal_requests_own ON withdrawal_requests;
DROP POLICY IF EXISTS withdrawal_requests_admin ON withdrawal_requests;
DROP POLICY IF EXISTS withdrawal_approvals_own ON withdrawal_approvals;
DROP POLICY IF EXISTS withdrawal_approvals_admin ON withdrawal_approvals;
DROP POLICY IF EXISTS withdrawal_limits_own ON withdrawal_limits;
DROP POLICY IF EXISTS withdrawal_limits_admin ON withdrawal_limits;
DROP POLICY IF EXISTS withdrawal_tracking_own ON withdrawal_tracking;
DROP POLICY IF EXISTS withdrawal_tracking_admin ON withdrawal_tracking;

-- Disable RLS
ALTER TABLE withdrawal_requests DISABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_approvals DISABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_limits DISABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_tracking DISABLE ROW LEVEL SECURITY;

-- Drop triggers
DROP TRIGGER IF EXISTS trigger_withdrawal_requests_updated_at ON withdrawal_requests;
DROP TRIGGER IF EXISTS trigger_withdrawal_limits_updated_at ON withdrawal_limits;
DROP TRIGGER IF EXISTS trigger_withdrawal_tracking_updated_at ON withdrawal_tracking;

-- Drop functions
DROP FUNCTION IF EXISTS update_withdrawal_updated_at();
DROP FUNCTION IF EXISTS check_withdrawal_limits(uuid, decimal);
DROP FUNCTION IF EXISTS update_withdrawal_tracking(uuid, decimal);

-- Drop indexes
DROP INDEX IF EXISTS idx_withdrawal_requests_user_id;
DROP INDEX IF EXISTS idx_withdrawal_requests_status;
DROP INDEX IF EXISTS idx_withdrawal_requests_expires_at;
DROP INDEX IF EXISTS idx_withdrawal_approvals_request_id;
DROP INDEX IF EXISTS idx_withdrawal_limits_user_id;
DROP INDEX IF EXISTS idx_withdrawal_tracking_user_date;

-- Drop tables
DROP TABLE IF EXISTS withdrawal_approvals;
DROP TABLE IF EXISTS withdrawal_requests;
DROP TABLE IF EXISTS withdrawal_limits;
DROP TABLE IF EXISTS withdrawal_tracking;

-- Drop enum
DROP TYPE IF EXISTS withdrawal_status;

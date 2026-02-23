-- Rollback withdrawal index improvements
DROP INDEX IF EXISTS idx_withdrawals_user_status;
DROP INDEX IF EXISTS idx_withdrawals_created_at;
DROP INDEX IF EXISTS idx_withdrawals_updated_at;
DROP INDEX IF EXISTS idx_withdrawals_user_status_created;

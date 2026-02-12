-- Rollback session optimization indexes
DROP INDEX IF EXISTS idx_sessions_token_active_expires;
DROP INDEX IF EXISTS idx_sessions_user_active;
DROP INDEX IF EXISTS idx_sessions_expires_inactive;

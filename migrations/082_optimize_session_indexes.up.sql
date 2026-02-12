-- Optimize session queries for performance

-- Composite index for session validation (token_hash + is_active + expires_at)
CREATE INDEX IF NOT EXISTS idx_sessions_token_active_expires 
ON sessions(token_hash, is_active, expires_at) 
WHERE is_active = true;

-- Index for active sessions by user (used in session limit enforcement)
-- Note: Cannot use NOW() in partial index predicate (not immutable)
CREATE INDEX IF NOT EXISTS idx_sessions_user_active 
ON sessions(user_id, created_at DESC) 
WHERE is_active = true;

-- Index for session cleanup queries
CREATE INDEX IF NOT EXISTS idx_sessions_expires_inactive 
ON sessions(expires_at) 
WHERE is_active = false;

-- Drop indexes
DROP INDEX IF EXISTS idx_chat_sessions_metadata_gin;
DROP INDEX IF EXISTS idx_chat_sessions_messages_gin;
DROP INDEX IF EXISTS idx_chat_sessions_user_last_accessed;
DROP INDEX IF EXISTS idx_chat_sessions_last_accessed;
DROP INDEX IF EXISTS idx_chat_sessions_user_status;
DROP INDEX IF EXISTS idx_chat_sessions_status;
DROP INDEX IF EXISTS idx_chat_sessions_user_id;

-- Drop table
DROP TABLE IF EXISTS chat_sessions;

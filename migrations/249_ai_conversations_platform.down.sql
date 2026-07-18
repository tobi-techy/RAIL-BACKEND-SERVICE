DROP INDEX IF EXISTS idx_ai_conversations_platform_thread;
ALTER TABLE ai_conversations
    DROP COLUMN IF EXISTS platform_identity_id,
    DROP COLUMN IF EXISTS platform_thread_id,
    DROP COLUMN IF EXISTS platform;

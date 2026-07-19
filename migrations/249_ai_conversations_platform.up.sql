ALTER TABLE ai_conversations
    ADD COLUMN IF NOT EXISTS platform VARCHAR(32),
    ADD COLUMN IF NOT EXISTS platform_thread_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS platform_identity_id UUID REFERENCES platform_identities(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_ai_conversations_platform_thread
    ON ai_conversations (platform, platform_thread_id)
    WHERE platform IS NOT NULL;

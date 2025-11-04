-- Create chat_sessions table for AI portfolio chat functionality
CREATE TABLE IF NOT EXISTS chat_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]'::jsonb,
    context JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMP NOT NULL DEFAULT NOW(),
    message_count INTEGER NOT NULL DEFAULT 0,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_address VARCHAR(255) NOT NULL,
    model VARCHAR(100) NOT NULL,
    auto_summarize BOOLEAN NOT NULL DEFAULT true,
    summarize_interval INTEGER NOT NULL DEFAULT 20
);

-- Create indexes for efficient queries
CREATE INDEX idx_chat_sessions_user_id ON chat_sessions(user_id);
CREATE INDEX idx_chat_sessions_status ON chat_sessions(status);
CREATE INDEX idx_chat_sessions_user_status ON chat_sessions(user_id, status);
CREATE INDEX idx_chat_sessions_last_accessed ON chat_sessions(last_accessed_at DESC);
CREATE INDEX idx_chat_sessions_user_last_accessed ON chat_sessions(user_id, last_accessed_at DESC);

-- Create GIN index for JSONB fields to enable efficient JSON queries
CREATE INDEX idx_chat_sessions_messages_gin ON chat_sessions USING GIN (messages);
CREATE INDEX idx_chat_sessions_metadata_gin ON chat_sessions USING GIN (metadata);

-- Add comment to table
COMMENT ON TABLE chat_sessions IS 'Stores AI chat sessions for portfolio discussions with full message history and context';
COMMENT ON COLUMN chat_sessions.messages IS 'JSONB array of chat messages with role, content, and metadata';
COMMENT ON COLUMN chat_sessions.context IS 'Portfolio context snapshot at session creation/update time';
COMMENT ON COLUMN chat_sessions.status IS 'Session status: active, archived, or summarized';
COMMENT ON COLUMN chat_sessions.provider_address IS '0G compute provider address for AI inference';
COMMENT ON COLUMN chat_sessions.tokens_used IS 'Total tokens consumed by this session';
COMMENT ON COLUMN chat_sessions.auto_summarize IS 'Whether to automatically compress session after threshold';
COMMENT ON COLUMN chat_sessions.summarize_interval IS 'Number of messages before triggering auto-summarization';

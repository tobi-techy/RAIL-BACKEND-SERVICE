CREATE TABLE IF NOT EXISTS ai_action_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    conversation_id UUID NOT NULL,
    action          VARCHAR(64) NOT NULL,
    params          JSONB NOT NULL DEFAULT '{}',
    status          VARCHAR(16) NOT NULL, -- executed, cancelled, failed, expired
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_action_audit_user_id ON ai_action_audit (user_id);
CREATE INDEX idx_ai_action_audit_created_at ON ai_action_audit (created_at);

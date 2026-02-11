-- Device-bound session management table
CREATE TABLE IF NOT EXISTS user_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL UNIQUE,
    binding_hash VARCHAR(255) NOT NULL,
    ip_address INET,
    user_agent TEXT,
    device_fingerprint VARCHAR(255),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_used_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    revoked_at TIMESTAMP WITH TIME ZONE
);

-- Indexes for performance
CREATE INDEX idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_device_id ON user_sessions(device_id);
CREATE INDEX idx_user_sessions_session_id ON user_sessions(session_id);
CREATE INDEX idx_user_sessions_active ON user_sessions(user_id, is_active) WHERE is_active = true;
CREATE INDEX idx_user_sessions_expires ON user_sessions(expires_at);
CREATE INDEX idx_user_sessions_binding_hash ON user_sessions(binding_hash);

-- Device binding audit table for security monitoring
CREATE TABLE IF NOT EXISTS device_binding_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id UUID REFERENCES user_sessions(id) ON DELETE SET NULL,
    device_id VARCHAR(255) NOT NULL,
    action VARCHAR(50) NOT NULL, -- 'session_created', 'session_revoked', 'device_mismatch', 'suspicious_activity'
    ip_address INET,
    user_agent TEXT,
    risk_score DECIMAL(5,4),
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for audit queries
CREATE INDEX idx_device_binding_audit_user_id ON device_binding_audit(user_id);
CREATE INDEX idx_device_binding_audit_device_id ON device_binding_audit(device_id);
CREATE INDEX idx_device_binding_audit_action ON device_binding_audit(action);
CREATE INDEX idx_device_binding_audit_created_at ON device_binding_audit(created_at);

-- Webhook event tracking for replay protection
CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(50) NOT NULL,
    event_id VARCHAR(255) NOT NULL,
    nonce VARCHAR(255),
    payload_hash VARCHAR(64) NOT NULL,
    ip_address INET,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(provider, event_id)
);

-- Index for webhook deduplication lookups
CREATE INDEX idx_webhook_events_provider_event ON webhook_events(provider, event_id);
CREATE INDEX idx_webhook_events_nonce ON webhook_events(provider, nonce) WHERE nonce IS NOT NULL;
CREATE INDEX idx_webhook_events_processed_at ON webhook_events(processed_at);

-- Cleanup old webhook events (keep 7 days) - use simple index, cleanup via scheduled job
CREATE INDEX idx_webhook_events_cleanup ON webhook_events(processed_at);

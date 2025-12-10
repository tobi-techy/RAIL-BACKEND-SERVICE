-- User rate limits table for per-user rate limiting
CREATE TABLE IF NOT EXISTS user_rate_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id VARCHAR(255) NOT NULL,
    endpoint VARCHAR(255) NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 1,
    window_start TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, endpoint, window_start)
);

CREATE INDEX IF NOT EXISTS idx_user_rate_limits_user_endpoint ON user_rate_limits(user_id, endpoint);
CREATE INDEX IF NOT EXISTS idx_user_rate_limits_window ON user_rate_limits(window_start);

-- Sessions table for token management and revocation
CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL,
    refresh_token_hash VARCHAR(64) NOT NULL,
    ip_address VARCHAR(45),
    user_agent TEXT,
    device_fingerprint VARCHAR(64),
    location VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_token ON sessions(refresh_token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(user_id, is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- Refresh token rotation tracking
CREATE TABLE IF NOT EXISTS refresh_token_rotations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    old_token_hash VARCHAR(64) NOT NULL,
    new_token_hash VARCHAR(64) NOT NULL,
    rotated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    ip_address VARCHAR(45),
    user_agent TEXT
);

CREATE INDEX IF NOT EXISTS idx_refresh_rotations_user ON refresh_token_rotations(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_rotations_old_token ON refresh_token_rotations(old_token_hash);

-- Secret rotation audit log
CREATE TABLE IF NOT EXISTS secret_rotations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    secret_name VARCHAR(255) NOT NULL,
    rotation_type VARCHAR(50) NOT NULL, -- 'scheduled', 'manual', 'emergency'
    rotated_by VARCHAR(255), -- user or system
    old_version VARCHAR(64), -- hash of old secret for audit
    new_version VARCHAR(64), -- hash of new secret for audit
    status VARCHAR(20) NOT NULL DEFAULT 'pending', -- 'pending', 'completed', 'failed', 'rolled_back'
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_secret_rotations_name ON secret_rotations(secret_name);
CREATE INDEX IF NOT EXISTS idx_secret_rotations_status ON secret_rotations(status);
CREATE INDEX IF NOT EXISTS idx_secret_rotations_created ON secret_rotations(created_at DESC);

-- Add bcrypt_cost column to track password hash cost for future upgrades
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'password_bcrypt_cost') THEN
        ALTER TABLE users ADD COLUMN password_bcrypt_cost INTEGER DEFAULT 10;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'password_expires_at') THEN
        ALTER TABLE users ADD COLUMN password_expires_at TIMESTAMP WITH TIME ZONE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'require_password_change') THEN
        ALTER TABLE users ADD COLUMN require_password_change BOOLEAN DEFAULT FALSE;
    END IF;
END $$;

-- Login attempt tracking (for persistent storage, Redis is primary)
CREATE TABLE IF NOT EXISTS login_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identifier VARCHAR(255) NOT NULL, -- email or phone
    ip_address VARCHAR(45) NOT NULL,
    user_agent TEXT,
    success BOOLEAN NOT NULL,
    failure_reason VARCHAR(100),
    captcha_required BOOLEAN DEFAULT FALSE,
    captcha_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_login_attempts_identifier ON login_attempts(identifier);
CREATE INDEX IF NOT EXISTS idx_login_attempts_ip ON login_attempts(ip_address);
CREATE INDEX IF NOT EXISTS idx_login_attempts_created ON login_attempts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_login_attempts_identifier_time ON login_attempts(identifier, created_at DESC);

-- Cleanup old login attempts (keep 30 days)
CREATE OR REPLACE FUNCTION cleanup_old_login_attempts() RETURNS void AS $$
BEGIN
    DELETE FROM login_attempts WHERE created_at < NOW() - INTERVAL '30 days';
END;
$$ LANGUAGE plpgsql;

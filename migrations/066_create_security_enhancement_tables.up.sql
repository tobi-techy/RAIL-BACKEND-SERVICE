-- Known devices table for device fingerprinting
CREATE TABLE IF NOT EXISTS known_devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fingerprint VARCHAR(64) NOT NULL,
    device_name VARCHAR(255) NOT NULL DEFAULT 'Unknown Device',
    ip_address VARCHAR(45),
    location VARCHAR(255),
    is_trusted BOOLEAN NOT NULL DEFAULT FALSE,
    last_used_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_known_devices_user_id ON known_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_known_devices_fingerprint ON known_devices(fingerprint);
CREATE INDEX IF NOT EXISTS idx_known_devices_last_used ON known_devices(last_used_at DESC);

-- IP whitelist table for sensitive operations
CREATE TABLE IF NOT EXISTS ip_whitelist (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address VARCHAR(45) NOT NULL,
    label VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    verified_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, ip_address)
);

CREATE INDEX IF NOT EXISTS idx_ip_whitelist_user_id ON ip_whitelist(user_id);
CREATE INDEX IF NOT EXISTS idx_ip_whitelist_active ON ip_whitelist(user_id, is_active) WHERE is_active = TRUE;

-- Withdrawal confirmations table for audit trail
CREATE TABLE IF NOT EXISTS withdrawal_confirmations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    withdrawal_id UUID NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    destination_address VARCHAR(255) NOT NULL,
    token_hash VARCHAR(128) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_withdrawal_confirmations_user_id ON withdrawal_confirmations(user_id);
CREATE INDEX IF NOT EXISTS idx_withdrawal_confirmations_token ON withdrawal_confirmations(token_hash);
CREATE INDEX IF NOT EXISTS idx_withdrawal_confirmations_withdrawal ON withdrawal_confirmations(withdrawal_id);

-- Security events table for audit and anomaly detection
CREATE TABLE IF NOT EXISTS security_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL DEFAULT 'info',
    ip_address VARCHAR(45),
    user_agent TEXT,
    device_fingerprint VARCHAR(64),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_security_events_user_id ON security_events(user_id);
CREATE INDEX IF NOT EXISTS idx_security_events_type ON security_events(event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_severity ON security_events(severity);
CREATE INDEX IF NOT EXISTS idx_security_events_created_at ON security_events(created_at DESC);

-- Password history table to prevent reuse
CREATE TABLE IF NOT EXISTS password_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_password_history_user_id ON password_history(user_id);

-- Add security-related columns to users table if not exists
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'security_level') THEN
        ALTER TABLE users ADD COLUMN security_level VARCHAR(20) DEFAULT 'standard';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'require_ip_whitelist') THEN
        ALTER TABLE users ADD COLUMN require_ip_whitelist BOOLEAN DEFAULT FALSE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'require_device_verification') THEN
        ALTER TABLE users ADD COLUMN require_device_verification BOOLEAN DEFAULT FALSE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'last_password_change') THEN
        ALTER TABLE users ADD COLUMN last_password_change TIMESTAMP WITH TIME ZONE;
    END IF;
END $$;

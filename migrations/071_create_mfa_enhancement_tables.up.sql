-- Enhanced MFA tables for multi-method authentication

-- SMS MFA verification codes
CREATE TABLE IF NOT EXISTS sms_mfa_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    phone_number VARCHAR(20) NOT NULL,
    code_hash VARCHAR(64) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sms_mfa_codes_user ON sms_mfa_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_sms_mfa_codes_expires ON sms_mfa_codes(expires_at);

-- User MFA preferences and settings
CREATE TABLE IF NOT EXISTS user_mfa_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE UNIQUE,
    primary_method VARCHAR(20) NOT NULL DEFAULT 'totp', -- totp, sms, webauthn
    fallback_method VARCHAR(20), -- backup method
    phone_number_encrypted VARCHAR(255),
    mfa_required BOOLEAN NOT NULL DEFAULT FALSE,
    mfa_enforced_at TIMESTAMP WITH TIME ZONE,
    grace_period_ends TIMESTAMP WITH TIME ZONE, -- allow login without MFA until this time
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_mfa_settings_user ON user_mfa_settings(user_id);

-- Geo-blocking and location tracking
CREATE TABLE IF NOT EXISTS geo_locations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address VARCHAR(45) NOT NULL,
    country_code VARCHAR(2),
    region VARCHAR(100),
    city VARCHAR(100),
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    is_vpn BOOLEAN DEFAULT FALSE,
    is_proxy BOOLEAN DEFAULT FALSE,
    is_tor BOOLEAN DEFAULT FALSE,
    risk_score DECIMAL(3, 2) DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_geo_locations_user ON geo_locations(user_id);
CREATE INDEX IF NOT EXISTS idx_geo_locations_country ON geo_locations(country_code);
CREATE INDEX IF NOT EXISTS idx_geo_locations_created ON geo_locations(created_at DESC);

-- Blocked countries configuration
CREATE TABLE IF NOT EXISTS blocked_countries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    country_code VARCHAR(2) NOT NULL UNIQUE,
    country_name VARCHAR(100) NOT NULL,
    reason VARCHAR(255),
    blocked_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    blocked_by VARCHAR(255)
);

-- Fraud detection scores and signals
CREATE TABLE IF NOT EXISTS fraud_signals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    session_id VARCHAR(64),
    signal_type VARCHAR(50) NOT NULL, -- velocity, geo_anomaly, device_anomaly, behavior, etc.
    signal_value DECIMAL(5, 4) NOT NULL, -- 0.0000 to 1.0000
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_fraud_signals_user ON fraud_signals(user_id);
CREATE INDEX IF NOT EXISTS idx_fraud_signals_type ON fraud_signals(signal_type);
CREATE INDEX IF NOT EXISTS idx_fraud_signals_created ON fraud_signals(created_at DESC);

-- User behavior patterns for anomaly detection
CREATE TABLE IF NOT EXISTS user_behavior_patterns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE UNIQUE,
    typical_login_hours JSONB DEFAULT '[]', -- array of typical hours [9, 10, 11, ...]
    typical_countries JSONB DEFAULT '[]', -- array of country codes
    typical_devices INTEGER DEFAULT 1,
    avg_session_duration INTEGER DEFAULT 0, -- seconds
    avg_transactions_per_day DECIMAL(10, 2) DEFAULT 0,
    avg_transaction_amount DECIMAL(20, 8) DEFAULT 0,
    last_analyzed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_behavior_user ON user_behavior_patterns(user_id);

-- Security incidents for incident response
CREATE TABLE IF NOT EXISTS security_incidents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_type VARCHAR(50) NOT NULL, -- breach_attempt, account_takeover, fraud, etc.
    severity VARCHAR(20) NOT NULL DEFAULT 'medium', -- low, medium, high, critical
    status VARCHAR(20) NOT NULL DEFAULT 'open', -- open, investigating, resolved, false_positive
    affected_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    affected_users_count INTEGER DEFAULT 1,
    description TEXT NOT NULL,
    detection_method VARCHAR(50), -- automated, manual, user_report
    indicators JSONB DEFAULT '{}', -- IOCs
    response_actions JSONB DEFAULT '[]', -- actions taken
    assigned_to VARCHAR(255),
    resolved_at TIMESTAMP WITH TIME ZONE,
    resolution_notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_security_incidents_type ON security_incidents(incident_type);
CREATE INDEX IF NOT EXISTS idx_security_incidents_severity ON security_incidents(severity);
CREATE INDEX IF NOT EXISTS idx_security_incidents_status ON security_incidents(status);
CREATE INDEX IF NOT EXISTS idx_security_incidents_created ON security_incidents(created_at DESC);

-- Incident response actions log
CREATE TABLE IF NOT EXISTS incident_response_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES security_incidents(id) ON DELETE CASCADE,
    action_type VARCHAR(50) NOT NULL, -- notify, block, reset_password, revoke_sessions, etc.
    action_details JSONB DEFAULT '{}',
    performed_by VARCHAR(255) NOT NULL, -- system or user email
    performed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_incident_response_incident ON incident_response_log(incident_id);

-- Add MFA-related columns to users table
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'mfa_enforced') THEN
        ALTER TABLE users ADD COLUMN mfa_enforced BOOLEAN DEFAULT FALSE;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'account_value_tier') THEN
        ALTER TABLE users ADD COLUMN account_value_tier VARCHAR(20) DEFAULT 'standard'; -- standard, premium, high_value
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'fraud_score') THEN
        ALTER TABLE users ADD COLUMN fraud_score DECIMAL(5, 4) DEFAULT 0;
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'last_fraud_check') THEN
        ALTER TABLE users ADD COLUMN last_fraud_check TIMESTAMP WITH TIME ZONE;
    END IF;
END $$;

-- Function to cleanup old fraud signals (keep 90 days)
CREATE OR REPLACE FUNCTION cleanup_old_fraud_signals() RETURNS void AS $$
BEGIN
    DELETE FROM fraud_signals WHERE created_at < NOW() - INTERVAL '90 days';
    DELETE FROM geo_locations WHERE created_at < NOW() - INTERVAL '90 days';
END;
$$ LANGUAGE plpgsql;

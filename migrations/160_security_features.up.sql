-- Transaction risk assessments (Feature 2)
CREATE TABLE IF NOT EXISTS transaction_risk_assessments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    transaction_type VARCHAR(50) NOT NULL,
    amount DECIMAL(20,8),
    risk_score DECIMAL(5,4) NOT NULL,
    risk_level VARCHAR(20) NOT NULL,
    action VARCHAR(20) NOT NULL,
    signals JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_tra_user_id ON transaction_risk_assessments(user_id);
CREATE INDEX idx_tra_created_at ON transaction_risk_assessments(created_at);

-- Whitelisted addresses (Feature 3)
CREATE TABLE IF NOT EXISTS whitelisted_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    chain VARCHAR(50) NOT NULL,
    address VARCHAR(255) NOT NULL,
    label VARCHAR(100),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    cooling_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, chain, address)
);
CREATE INDEX idx_wa_user_id ON whitelisted_addresses(user_id);

-- Session anomalies (Feature 4)
CREATE TABLE IF NOT EXISTS session_anomalies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    anomaly_type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    details JSONB DEFAULT '{}',
    ip_address VARCHAR(45),
    country VARCHAR(10),
    city VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sa_user_id ON session_anomalies(user_id);
CREATE INDEX idx_sa_created_at ON session_anomalies(created_at);

-- Withdrawal limit usage (Feature 5)
CREATE TABLE IF NOT EXISTS withdrawal_limit_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    amount DECIMAL(20,8) NOT NULL,
    period_type VARCHAR(10) NOT NULL,
    period_key VARCHAR(20) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_wlu_user_period ON withdrawal_limit_usage(user_id, period_type, period_key);

-- MFA challenges (Feature 7)
CREATE TABLE IF NOT EXISTS mfa_challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    challenge_type VARCHAR(20) NOT NULL,
    code_hash VARCHAR(128) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_mfa_user_expires ON mfa_challenges(user_id, expires_at);

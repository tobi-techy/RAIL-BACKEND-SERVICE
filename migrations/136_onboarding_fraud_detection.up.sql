-- Cross-account device/network correlation for post-KYC fraud detection.
-- Detects fraud rings that pass KYC with purchased identities by correlating
-- device fingerprints and IPs across multiple accounts.

-- device_account_links: records every device→account association.
-- The key query is: "how many distinct user_ids share this fingerprint?"
CREATE TABLE IF NOT EXISTS device_account_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_fingerprint VARCHAR(64) NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address VARCHAR(45) NOT NULL,
    user_agent TEXT,
    event_type VARCHAR(20) NOT NULL, -- 'onboarding', 'deposit', 'login'
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Cross-account correlation: find all accounts sharing a device.
CREATE INDEX idx_device_account_links_fingerprint
    ON device_account_links(device_fingerprint, created_at DESC);

-- Per-user history.
CREATE INDEX idx_device_account_links_user
    ON device_account_links(user_id, created_at DESC);

-- IP-based correlation: find all accounts from same IP.
CREATE INDEX idx_device_account_links_ip
    ON device_account_links(ip_address, event_type, created_at DESC);

-- onboarding_risk_assessments: audit trail of every fraud assessment.
CREATE TABLE IF NOT EXISTS onboarding_risk_assessments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(30) NOT NULL, -- 'onboarding_complete', 'first_deposit'
    risk_score DECIMAL(5, 4) NOT NULL DEFAULT 0,
    risk_level VARCHAR(20) NOT NULL DEFAULT 'low',
    action VARCHAR(20) NOT NULL DEFAULT 'allow',
    signals JSONB DEFAULT '{}',
    ip_address VARCHAR(45),
    device_fingerprint VARCHAR(64),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_onboarding_risk_user
    ON onboarding_risk_assessments(user_id, created_at DESC);

CREATE INDEX idx_onboarding_risk_action
    ON onboarding_risk_assessments(action) WHERE action != 'allow';

-- Cleanup: remove device_account_links older than 90 days.
CREATE OR REPLACE FUNCTION cleanup_old_device_account_links() RETURNS void AS $$
BEGIN
    DELETE FROM device_account_links WHERE created_at < NOW() - INTERVAL '90 days';
    DELETE FROM onboarding_risk_assessments WHERE created_at < NOW() - INTERVAL '365 days';
END;
$$ LANGUAGE plpgsql;

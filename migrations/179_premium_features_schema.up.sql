-- Premium Features Schema
-- Tier 1.2: Black Tax Optimizer
CREATE TABLE IF NOT EXISTS family_support_budgets (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    monthly_limit DECIMAL(20,8) NOT NULL DEFAULT 0,
    alert_threshold_pct INT NOT NULL DEFAULT 80,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS family_support_recipients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_name VARCHAR(255) NOT NULL,
    recipient_identifier VARCHAR(255) NOT NULL,
    relationship VARCHAR(100),
    monthly_average DECIMAL(20,8) NOT NULL DEFAULT 0,
    total_sent_lifetime DECIMAL(20,8) NOT NULL DEFAULT 0,
    last_sent_at TIMESTAMPTZ,
    send_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, recipient_identifier)
);

CREATE INDEX idx_family_support_recipients_user_id ON family_support_recipients(user_id);

-- Tier 2.1: Scam Intelligence
CREATE TABLE IF NOT EXISTS merchant_risk_patterns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern VARCHAR(500) NOT NULL,
    risk_level VARCHAR(50) NOT NULL DEFAULT 'medium',
    category VARCHAR(100),
    description TEXT,
    report_count INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_merchant_risk_patterns_level ON merchant_risk_patterns(risk_level);

CREATE TABLE IF NOT EXISTS user_scam_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    merchant_name VARCHAR(500) NOT NULL,
    transaction_id UUID,
    alert_type VARCHAR(100) NOT NULL,
    risk_level VARCHAR(50) NOT NULL,
    reason TEXT,
    dismissed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_scam_alerts_user_id ON user_scam_alerts(user_id);
CREATE INDEX idx_user_scam_alerts_dismissed ON user_scam_alerts(user_id, dismissed);

-- Tier 2.2: Diaspora Tax Residency Tracker
CREATE TABLE IF NOT EXISTS user_location_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    country VARCHAR(2) NOT NULL,
    entered_at TIMESTAMPTZ NOT NULL,
    exited_at TIMESTAMPTZ,
    source VARCHAR(100) NOT NULL DEFAULT 'manual',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_location_logs_user_country ON user_location_logs(user_id, country);
CREATE INDEX idx_user_location_logs_entered ON user_location_logs(user_id, entered_at);

CREATE TABLE IF NOT EXISTS user_tax_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    primary_tax_country VARCHAR(2) NOT NULL DEFAULT 'NG',
    secondary_tax_country VARCHAR(2),
    days_in_primary INT NOT NULL DEFAULT 0,
    days_in_secondary INT NOT NULL DEFAULT 0,
    alert_threshold INT NOT NULL DEFAULT 150,
    tax_year_start_month INT NOT NULL DEFAULT 1,
    tax_year_start_day INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tier 3.1: Financial Trauma Detection
CREATE TABLE IF NOT EXISTS behavioral_health_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    metric_type VARCHAR(100) NOT NULL,
    value DECIMAL(10,4) NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_behavioral_health_metrics_user ON behavioral_health_metrics(user_id, metric_type, recorded_at);

CREATE TABLE IF NOT EXISTS financial_wellness_scores (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    overall_score DECIMAL(10,4) NOT NULL,
    anxiety_score DECIMAL(10,4) NOT NULL,
    avoidance_score DECIMAL(10,4) NOT NULL,
    impulsivity_score DECIMAL(10,4) NOT NULL,
    resilience_score DECIMAL(10,4) NOT NULL,
    recommendation TEXT,
    calculated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Tier 3.3: Panic Button
CREATE TABLE IF NOT EXISTS emergency_contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    phone VARCHAR(50) NOT NULL,
    email VARCHAR(255),
    relation VARCHAR(100),
    priority INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_emergency_contacts_user ON emergency_contacts(user_id, priority);

CREATE TABLE IF NOT EXISTS emergency_locks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    locked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    unlocked_at TIMESTAMPTZ,
    reason VARCHAR(100) NOT NULL,
    triggered_by VARCHAR(100) NOT NULL DEFAULT 'user',
    card_frozen BOOLEAN NOT NULL DEFAULT FALSE,
    stash_moved BOOLEAN NOT NULL DEFAULT FALSE,
    contacts_alerted BOOLEAN NOT NULL DEFAULT FALSE,
    alerted_contacts TEXT[],
    resolved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_emergency_locks_user ON emergency_locks(user_id, resolved);

-- Shared: Receipt Splitting
CREATE TABLE IF NOT EXISTS receipt_splits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    receipt_id UUID NOT NULL REFERENCES receipt_scans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    total_amount DECIMAL(20,8) NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS receipt_split_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    split_id UUID NOT NULL REFERENCES receipt_splits(id) ON DELETE CASCADE,
    item_name VARCHAR(255) NOT NULL,
    amount DECIMAL(20,8) NOT NULL,
    assigned_to VARCHAR(255) NOT NULL,
    paid BOOLEAN NOT NULL DEFAULT FALSE,
    p2p_transfer_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_receipt_split_items_split ON receipt_split_items(split_id);

-- Shared: Visa Proof Generator
CREATE TABLE IF NOT EXISTS visa_proof_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    visa_country VARCHAR(2) NOT NULL,
    visa_type VARCHAR(100) NOT NULL,
    bank_balance DECIMAL(20,8) NOT NULL,
    stash_balance DECIMAL(20,8) NOT NULL,
    total_holdings DECIMAL(20,8) NOT NULL,
    avg_monthly_inflow DECIMAL(20,8) NOT NULL,
    document_url TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'generating',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_visa_proof_requests_user ON visa_proof_requests(user_id, status);

-- Shared: Currency Exchange Rates
CREATE TABLE IF NOT EXISTS currency_exchange_rates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_code VARCHAR(3) NOT NULL,
    to_code VARCHAR(3) NOT NULL,
    rate DECIMAL(20,8) NOT NULL,
    date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(from_code, to_code, date)
);

CREATE INDEX idx_currency_rates_lookup ON currency_exchange_rates(from_code, to_code, date);

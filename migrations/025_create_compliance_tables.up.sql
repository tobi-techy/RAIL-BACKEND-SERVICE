-- Transaction Limits
CREATE TABLE IF NOT EXISTS transaction_limits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    limit_type VARCHAR(50) NOT NULL,
    period VARCHAR(20) NOT NULL,
    max_amount DECIMAL(20, 8) NOT NULL,
    used_amount DECIMAL(20, 8) DEFAULT 0,
    reset_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_transaction_limits_user_id ON transaction_limits(user_id);
CREATE INDEX idx_transaction_limits_type ON transaction_limits(limit_type);

-- Fraud Alerts
CREATE TABLE IF NOT EXISTS fraud_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tx_id UUID NOT NULL,
    risk_score DECIMAL(5, 2) NOT NULL,
    risk_factors JSONB,
    status VARCHAR(50) DEFAULT 'pending',
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_fraud_alerts_user_id ON fraud_alerts(user_id);
CREATE INDEX idx_fraud_alerts_status ON fraud_alerts(status);

-- Notifications
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    channel VARCHAR(20) NOT NULL,
    priority VARCHAR(20) NOT NULL,
    title VARCHAR(255) NOT NULL,
    message TEXT NOT NULL,
    data JSONB,
    read BOOLEAN DEFAULT FALSE,
    sent_at TIMESTAMP,
    read_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_notifications_user_id ON notifications(user_id);
CREATE INDEX idx_notifications_read ON notifications(read);

-- User Preferences
CREATE TABLE IF NOT EXISTS user_preferences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    email_notifications BOOLEAN DEFAULT TRUE,
    push_notifications BOOLEAN DEFAULT TRUE,
    sms_notifications BOOLEAN DEFAULT FALSE,
    deposit_alerts BOOLEAN DEFAULT TRUE,
    withdrawal_alerts BOOLEAN DEFAULT TRUE,
    trade_alerts BOOLEAN DEFAULT TRUE,
    security_alerts BOOLEAN DEFAULT TRUE,
    portfolio_updates BOOLEAN DEFAULT TRUE,
    marketing_emails BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Note: kyc_submissions and kyc_documents tables already exist from migration 001 and 003
-- Skipping duplicate table creation

-- AML Checks
CREATE TABLE IF NOT EXISTS aml_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    check_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL,
    risk_level VARCHAR(50) NOT NULL,
    provider_ref VARCHAR(255),
    results JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_aml_checks_user_id ON aml_checks(user_id);

-- Note: audit_logs table already exists from previous migrations
-- Skipping duplicate table creation

-- Data Privacy Requests
CREATE TABLE IF NOT EXISTS data_privacy_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) DEFAULT 'pending',
    completed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_data_privacy_requests_user_id ON data_privacy_requests(user_id);

-- Portfolio Rebalances
CREATE TABLE IF NOT EXISTS portfolio_rebalances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    portfolio_id UUID NOT NULL,
    strategy VARCHAR(50) NOT NULL,
    target_allocations JSONB NOT NULL,
    current_allocations JSONB NOT NULL,
    trades JSONB,
    status VARCHAR(50) DEFAULT 'pending',
    executed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_portfolio_rebalances_portfolio_id ON portfolio_rebalances(portfolio_id);

-- Tax Reports
CREATE TABLE IF NOT EXISTS tax_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tax_year INTEGER NOT NULL,
    total_gains DECIMAL(20, 8) DEFAULT 0,
    total_losses DECIMAL(20, 8) DEFAULT 0,
    short_term_gains DECIMAL(20, 8) DEFAULT 0,
    long_term_gains DECIMAL(20, 8) DEFAULT 0,
    report_url TEXT,
    generated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tax_reports_user_id ON tax_reports(user_id);
CREATE INDEX idx_tax_reports_year ON tax_reports(tax_year);

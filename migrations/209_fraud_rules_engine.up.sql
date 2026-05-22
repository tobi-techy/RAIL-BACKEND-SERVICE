-- Configurable fraud rules engine: admin-managed rules evaluated at runtime.
-- Sanctions screening, fund-through detection, account freeze automation.

-- fraud_rules: configurable rules that can be toggled without code deploys.
CREATE TABLE IF NOT EXISTS fraud_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    description TEXT,
    rule_type VARCHAR(50) NOT NULL, -- 'velocity', 'amount', 'pattern', 'geo', 'device', 'custom'
    conditions JSONB NOT NULL DEFAULT '{}',
    action VARCHAR(30) NOT NULL DEFAULT 'flag', -- 'allow', 'flag', 'block', 'freeze', 'manual_review'
    severity VARCHAR(20) NOT NULL DEFAULT 'medium', -- 'low', 'medium', 'high', 'critical'
    score_weight DECIMAL(3,2) NOT NULL DEFAULT 1.0,
    is_active BOOLEAN NOT NULL DEFAULT true,
    applies_to VARCHAR(30) NOT NULL DEFAULT 'all', -- 'deposit', 'withdrawal', 'transfer', 'all'
    created_by VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_fraud_rules_active ON fraud_rules(is_active, applies_to);

-- fraud_alerts: real-time alerts generated when rules trigger.
CREATE TABLE IF NOT EXISTS fraud_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    rule_id UUID REFERENCES fraud_rules(id),
    alert_type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'open', -- 'open', 'investigating', 'resolved', 'dismissed'
    details JSONB DEFAULT '{}',
    transaction_id UUID,
    transaction_type VARCHAR(30),
    amount DECIMAL(20,8),
    resolved_by VARCHAR(100),
    resolved_at TIMESTAMPTZ,
    resolution_notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_fraud_alerts_user ON fraud_alerts(user_id, created_at DESC);
CREATE INDEX idx_fraud_alerts_status ON fraud_alerts(status, severity);
CREATE INDEX idx_fraud_alerts_created ON fraud_alerts(created_at DESC);

-- account_freezes: tracks when accounts are frozen and why.
CREATE TABLE IF NOT EXISTS account_freezes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    freeze_type VARCHAR(30) NOT NULL, -- 'fraud_score', 'sanctions', 'manual', 'fund_through', 'rule_trigger'
    reason TEXT NOT NULL,
    triggered_by VARCHAR(100) NOT NULL, -- 'system', 'admin:<email>', 'rule:<rule_id>'
    alert_id UUID REFERENCES fraud_alerts(id),
    is_active BOOLEAN NOT NULL DEFAULT true,
    unfrozen_by VARCHAR(100),
    unfrozen_at TIMESTAMPTZ,
    unfreeze_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_account_freezes_user ON account_freezes(user_id, is_active);
CREATE INDEX idx_account_freezes_active ON account_freezes(is_active) WHERE is_active = true;

-- sanctions_checks: records of sanctions/watchlist screening.
CREATE TABLE IF NOT EXISTS sanctions_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    check_type VARCHAR(30) NOT NULL, -- 'onboarding', 'periodic', 'transaction', 'manual'
    full_name VARCHAR(255) NOT NULL,
    lists_checked TEXT[] NOT NULL DEFAULT ARRAY['OFAC', 'UN', 'EU'],
    match_found BOOLEAN NOT NULL DEFAULT false,
    match_details JSONB,
    match_score DECIMAL(5,4) DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'clear', -- 'clear', 'potential_match', 'confirmed_match', 'false_positive'
    reviewed_by VARCHAR(100),
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sanctions_checks_user ON sanctions_checks(user_id, created_at DESC);
CREATE INDEX idx_sanctions_checks_match ON sanctions_checks(match_found, status);

-- fund_through_detections: tracks deposit→immediate withdrawal patterns.
CREATE TABLE IF NOT EXISTS fund_through_detections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    deposit_id UUID,
    withdrawal_id UUID,
    deposit_amount DECIMAL(20,8) NOT NULL,
    withdrawal_amount DECIMAL(20,8) NOT NULL,
    time_between_seconds INTEGER NOT NULL,
    withdrawal_ratio DECIMAL(5,4) NOT NULL, -- withdrawal_amount / deposit_amount
    risk_score DECIMAL(5,4) NOT NULL,
    action_taken VARCHAR(30) NOT NULL, -- 'flagged', 'blocked', 'frozen'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_fund_through_user ON fund_through_detections(user_id, created_at DESC);

-- Add columns safely: add without NOT NULL, backfill, then add constraint
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'withdrawals_frozen') THEN
        ALTER TABLE users ADD COLUMN withdrawals_frozen BOOLEAN DEFAULT false;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'deposits_frozen') THEN
        ALTER TABLE users ADD COLUMN deposits_frozen BOOLEAN DEFAULT false;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'account_frozen_at') THEN
        ALTER TABLE users ADD COLUMN account_frozen_at TIMESTAMPTZ;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'sanctions_status') THEN
        ALTER TABLE users ADD COLUMN sanctions_status VARCHAR(20) DEFAULT 'clear';
    END IF;
END $$;

-- Backfill NULLs and add NOT NULL constraints
UPDATE users SET withdrawals_frozen = false WHERE withdrawals_frozen IS NULL;
UPDATE users SET deposits_frozen = false WHERE deposits_frozen IS NULL;
UPDATE users SET sanctions_status = 'clear' WHERE sanctions_status IS NULL;
ALTER TABLE users ALTER COLUMN withdrawals_frozen SET NOT NULL;
ALTER TABLE users ALTER COLUMN deposits_frozen SET NOT NULL;
ALTER TABLE users ALTER COLUMN sanctions_status SET NOT NULL;

-- Seed default fraud rules (ensure severity column exists from prior partial runs)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'fraud_rules' AND column_name = 'severity') THEN
        ALTER TABLE fraud_rules ADD COLUMN severity VARCHAR(20) NOT NULL DEFAULT 'medium';
    END IF;
END $$;

INSERT INTO fraud_rules (name, description, rule_type, conditions, action, severity, score_weight, applies_to) VALUES
('High velocity deposits', 'More than 5 deposits in 1 hour', 'velocity', '{"event": "deposit", "count_threshold": 5, "window_seconds": 3600}', 'flag', 'medium', 1.0, 'deposit'),
('Large first deposit', 'First deposit over $5000 on account less than 24h old', 'amount', '{"min_amount": 5000, "max_account_age_hours": 24, "first_transaction": true}', 'manual_review', 'high', 1.5, 'deposit'),
('Rapid fund-through', 'Withdrawal of >80% balance within 1 hour of deposit', 'pattern', '{"pattern": "fund_through", "withdrawal_ratio": 0.8, "max_delay_seconds": 3600}', 'block', 'critical', 2.0, 'withdrawal'),
('New device large withdrawal', 'Withdrawal >$1000 from device registered <24h ago', 'device', '{"min_amount": 1000, "max_device_age_hours": 24}', 'manual_review', 'high', 1.5, 'withdrawal'),
('Multiple accounts same device', 'Device linked to 3+ accounts', 'device', '{"max_accounts_per_device": 3}', 'freeze', 'critical', 2.0, 'all'),
('Unusual hour large transaction', 'Transaction >$2000 between 1am-5am local time', 'custom', '{"min_amount": 2000, "hour_start": 1, "hour_end": 5}', 'flag', 'medium', 0.8, 'all'),
('Daily withdrawal limit breach', 'Cumulative withdrawals exceed $50000 in 24h', 'velocity', '{"event": "withdrawal", "sum_threshold": 50000, "window_seconds": 86400}', 'block', 'critical', 2.0, 'withdrawal'),
('Structuring detection', 'Multiple deposits just under $10000 reporting threshold', 'pattern', '{"pattern": "structuring", "threshold": 10000, "margin": 500, "count": 3, "window_hours": 48}', 'manual_review', 'critical', 2.5, 'deposit')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS financial_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    primary_currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    income_frequency VARCHAR(20) NOT NULL DEFAULT 'unknown'
        CHECK (income_frequency IN ('unknown', 'weekly', 'biweekly', 'monthly', 'irregular')),
    monthly_income DECIMAL(20, 2) NOT NULL DEFAULT 0 CHECK (monthly_income >= 0),
    monthly_fixed_costs DECIMAL(20, 2) NOT NULL DEFAULT 0 CHECK (monthly_fixed_costs >= 0),
    monthly_savings_target DECIMAL(20, 2) NOT NULL DEFAULT 0 CHECK (monthly_savings_target >= 0),
    emergency_fund_target DECIMAL(20, 2) NOT NULL DEFAULT 0 CHECK (emergency_fund_target >= 0),
    risk_tolerance VARCHAR(20) NOT NULL DEFAULT 'unknown'
        CHECK (risk_tolerance IN ('unknown', 'low', 'medium', 'high')),
    investment_horizon VARCHAR(20) NOT NULL DEFAULT 'unknown'
        CHECK (investment_horizon IN ('unknown', 'short_term', 'medium_term', 'long_term')),
    financial_goal TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_financial_profiles_risk_tolerance
    ON financial_profiles (risk_tolerance);

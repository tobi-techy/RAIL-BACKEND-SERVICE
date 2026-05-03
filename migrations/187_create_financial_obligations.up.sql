CREATE TABLE IF NOT EXISTS financial_obligations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(40) NOT NULL CHECK (type IN (
        'debt', 'invoice', 'payroll', 'insurance', 'education', 'rent',
        'family_support', 'tax', 'subscription', 'vendor_bill', 'other'
    )),
    name VARCHAR(200) NOT NULL,
    amount DECIMAL(20, 2) NOT NULL CHECK (amount > 0),
    currency VARCHAR(10) NOT NULL,
    cadence VARCHAR(20) NOT NULL CHECK (cadence IN (
        'one_time', 'weekly', 'biweekly', 'monthly', 'quarterly', 'annual'
    )),
    due_date TIMESTAMP WITH TIME ZONE,
    due_day INTEGER CHECK (due_day IS NULL OR (due_day >= 1 AND due_day <= 31)),
    priority VARCHAR(20) NOT NULL DEFAULT 'medium' CHECK (priority IN ('critical', 'high', 'medium', 'low')),
    counterparty VARCHAR(200),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'paid', 'cancelled')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_financial_obligations_user_status
    ON financial_obligations (user_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_financial_obligations_user_type_status
    ON financial_obligations (user_id, type, status);

CREATE INDEX IF NOT EXISTS idx_financial_obligations_due_date
    ON financial_obligations (user_id, due_date)
    WHERE due_date IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_financial_obligations_due_day
    ON financial_obligations (user_id, due_day)
    WHERE due_day IS NOT NULL;

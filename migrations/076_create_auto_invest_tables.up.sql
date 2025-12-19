-- Auto-invest settings table
CREATE TABLE IF NOT EXISTS auto_invest_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    basket_id UUID NOT NULL REFERENCES baskets(id),
    threshold DECIMAL(20, 8) NOT NULL DEFAULT 10.00,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Auto-invest events table for audit trail
CREATE TABLE IF NOT EXISTS auto_invest_events (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    basket_id UUID NOT NULL REFERENCES baskets(id),
    amount DECIMAL(20, 8) NOT NULL,
    order_id UUID NOT NULL REFERENCES orders(id),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_auto_invest_settings_enabled ON auto_invest_settings(enabled) WHERE enabled = true;
CREATE INDEX idx_auto_invest_events_user_id ON auto_invest_events(user_id);
CREATE INDEX idx_auto_invest_events_status ON auto_invest_events(status);
CREATE INDEX idx_auto_invest_events_created_at ON auto_invest_events(created_at);

-- Trigger for updated_at
CREATE TRIGGER update_auto_invest_settings_updated_at
    BEFORE UPDATE ON auto_invest_settings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

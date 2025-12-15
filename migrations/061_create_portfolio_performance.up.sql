-- Portfolio Performance Tracking Migration
-- This migration creates a table to track daily portfolio performance (NAV and P&L)

CREATE TABLE IF NOT EXISTS portfolio_performance (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    nav DECIMAL(36, 18) NOT NULL DEFAULT 0,
    pnl DECIMAL(36, 18) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, date)
);

CREATE INDEX idx_portfolio_performance_user_id ON portfolio_performance(user_id);
CREATE INDEX idx_portfolio_performance_date ON portfolio_performance(date);
CREATE INDEX idx_portfolio_performance_user_date ON portfolio_performance(user_id, date DESC);

COMMENT ON TABLE portfolio_performance IS 'Tracks daily portfolio Net Asset Value (NAV) and Profit & Loss (P&L) for users';
COMMENT ON COLUMN portfolio_performance.nav IS 'Net Asset Value - total portfolio value on this date';
COMMENT ON COLUMN portfolio_performance.pnl IS 'Profit & Loss - unrealized gains/losses on this date';

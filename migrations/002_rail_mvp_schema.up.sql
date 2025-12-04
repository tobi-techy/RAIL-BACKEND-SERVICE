-- STACK MVP Database Schema (aligned with architecture document)
-- This migration updates the schema to match the architecture specification

-- Drop existing tables that don't match the new schema
DROP TABLE IF EXISTS basket_allocations;
DROP TABLE IF EXISTS baskets;
DROP TABLE IF EXISTS balances;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS tokens;

-- Create updated tables matching architecture specification

-- Wallets table (aligned with architecture)
CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain VARCHAR(20) NOT NULL CHECK (chain IN ('Aptos', 'Solana', 'polygon', 'starknet')),
    address VARCHAR(100) NOT NULL UNIQUE,
    provider_ref VARCHAR(200) NOT NULL, -- Reference to wallet manager (Circle, etc.)
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, chain)
);

-- Create indexes for wallets
CREATE INDEX idx_wallets_user_id ON wallets(user_id);
CREATE INDEX idx_wallets_chain ON wallets(chain);
CREATE INDEX idx_wallets_address ON wallets(address);
CREATE INDEX idx_wallets_provider_ref ON wallets(provider_ref);

-- Deposits table (stablecoin deposits)
CREATE TABLE deposits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain VARCHAR(20) NOT NULL CHECK (chain IN ('Aptos', 'Solana', 'polygon', 'starknet')),
    tx_hash VARCHAR(100) NOT NULL UNIQUE,
    token VARCHAR(20) NOT NULL CHECK (token IN ('USDC')),
    amount DECIMAL(36, 18) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for deposits
CREATE INDEX idx_deposits_user_id ON deposits(user_id);
CREATE INDEX idx_deposits_tx_hash ON deposits(tx_hash);
CREATE INDEX idx_deposits_status ON deposits(status);
CREATE INDEX idx_deposits_created_at ON deposits(created_at);

-- Balances table (user buying power)
CREATE TABLE balances (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    buying_power DECIMAL(36, 18) NOT NULL DEFAULT 0,
    pending_deposits DECIMAL(36, 18) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Baskets table (curated investment baskets)
CREATE TABLE baskets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL,
    risk_level VARCHAR(20) NOT NULL CHECK (risk_level IN ('conservative', 'balanced', 'growth')),
    composition_json JSONB NOT NULL, -- Array of {symbol: string, weight: decimal}
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for baskets
CREATE INDEX idx_baskets_risk_level ON baskets(risk_level);
CREATE INDEX idx_baskets_name ON baskets(name);

-- Orders table (investment orders)
CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    basket_id UUID NOT NULL REFERENCES baskets(id) ON DELETE CASCADE,
    side VARCHAR(10) NOT NULL CHECK (side IN ('buy', 'sell')),
    amount DECIMAL(36, 18) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'accepted' CHECK (status IN ('accepted', 'pending', 'partially_filled', 'filled', 'failed', 'canceled')),
    brokerage_ref VARCHAR(200), -- Reference to brokerage order
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for orders
CREATE INDEX idx_orders_user_id ON orders(user_id);
CREATE INDEX idx_orders_basket_id ON orders(basket_id);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_created_at ON orders(created_at);

-- Positions table (user positions in baskets)
CREATE TABLE positions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    basket_id UUID NOT NULL REFERENCES baskets(id) ON DELETE CASCADE,
    quantity DECIMAL(36, 18) NOT NULL DEFAULT 0,
    avg_price DECIMAL(36, 18) NOT NULL DEFAULT 0,
    market_value DECIMAL(36, 18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, basket_id)
);

-- Create indexes for positions
CREATE INDEX idx_positions_user_id ON positions(user_id);
CREATE INDEX idx_positions_basket_id ON positions(basket_id);

-- Portfolio performance tracking
CREATE TABLE portfolio_perf (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    nav DECIMAL(36, 18) NOT NULL, -- Net Asset Value
    pnl DECIMAL(36, 18) NOT NULL, -- Profit & Loss
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    PRIMARY KEY(user_id, date)
);

-- Create indexes for portfolio_perf
CREATE INDEX idx_portfolio_perf_user_id ON portfolio_perf(user_id);
CREATE INDEX idx_portfolio_perf_date ON portfolio_perf(date);

-- AI summaries table (AI-generated insights)
CREATE TABLE ai_summaries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    week_start DATE NOT NULL, -- Start of the week for the summary
    summary_md TEXT NOT NULL, -- Markdown content
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, week_start)
);

-- Create indexes for ai_summaries
CREATE INDEX idx_ai_summaries_user_id ON ai_summaries(user_id);
CREATE INDEX idx_ai_summaries_week_start ON ai_summaries(week_start);

-- Update audit_logs table to match architecture
ALTER TABLE audit_logs 
    DROP COLUMN IF EXISTS user_agent,
    ADD COLUMN entity VARCHAR(100),
    ADD COLUMN before JSONB,
    ADD COLUMN after JSONB;

-- Rename some columns to match architecture
ALTER TABLE audit_logs
    RENAME COLUMN created_at TO at;

-- Update triggers for new tables
CREATE TRIGGER update_wallets_updated_at BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_baskets_updated_at BEFORE UPDATE ON baskets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_balances_updated_at BEFORE UPDATE ON balances FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_orders_updated_at BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_positions_updated_at BEFORE UPDATE ON positions FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert sample curated baskets for MVP
INSERT INTO baskets (id, name, description, risk_level, composition_json) VALUES
(
    '123e4567-e89b-12d3-a456-426614174001'::uuid,
    'Tech Growth',
    'High-growth technology companies with strong fundamentals',
    'growth',
    '[{"symbol": "VTI", "weight": 0.4}, {"symbol": "QQQ", "weight": 0.3}, {"symbol": "ARKK", "weight": 0.2}, {"symbol": "MSFT", "weight": 0.1}]'::jsonb
),
(
    '123e4567-e89b-12d3-a456-426614174002'::uuid,
    'Balanced Growth',
    'Diversified portfolio balancing growth and stability',
    'balanced',
    '[{"symbol": "VTI", "weight": 0.5}, {"symbol": "BND", "weight": 0.3}, {"symbol": "VEA", "weight": 0.2}]'::jsonb
),
(
    '123e4567-e89b-12d3-a456-426614174003'::uuid,
    'Conservative Income',
    'Focus on dividend income and capital preservation',
    'conservative',
    '[{"symbol": "BND", "weight": 0.6}, {"symbol": "VYM", "weight": 0.3}, {"symbol": "VTEB", "weight": 0.1}]'::jsonb
),
(
    '123e4567-e89b-12d3-a456-426614174004'::uuid,
    'Sustainability Focus',
    'ESG-focused investments for sustainable returns',
    'balanced',
    '[{"symbol": "ESGV", "weight": 0.4}, {"symbol": "ICLN", "weight": 0.3}, {"symbol": "DSI", "weight": 0.3}]'::jsonb
),
(
    '123e4567-e89b-12d3-a456-426614174005'::uuid,
    'Global Diversified',
    'Worldwide exposure across developed and emerging markets',
    'balanced',
    '[{"symbol": "VTI", "weight": 0.4}, {"symbol": "VTIAX", "weight": 0.3}, {"symbol": "VWO", "weight": 0.3}]'::jsonb
);
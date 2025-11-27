-- AI Financial Manager Tables Migration
-- This migration creates all required tables for the AI Financial Manager feature including:
-- - User contributions tracking (round-ups, cashback, deposits)
-- - Investment streak tracking
-- - Personalized news storage
-- - AI chat sessions and messages
-- - Basket recommendations
-- - Rebalance previews
-- - Enhanced ai_summaries columns

-- User activity tracking (round-ups, cashback, deposits)
CREATE TABLE IF NOT EXISTS user_contributions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(20) NOT NULL CHECK (type IN ('deposit', 'roundup', 'cashback', 'referral')),
    amount DECIMAL(36, 18) NOT NULL CHECK (amount >= 0),
    source VARCHAR(100),
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_contributions_user_id ON user_contributions(user_id);
CREATE INDEX idx_user_contributions_type ON user_contributions(type);
CREATE INDEX idx_user_contributions_created_at ON user_contributions(created_at);

-- Investment streak tracking
CREATE TABLE IF NOT EXISTS investment_streaks (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    current_streak INT NOT NULL DEFAULT 0,
    longest_streak INT NOT NULL DEFAULT 0,
    last_investment_date DATE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_investment_streaks_updated_at ON investment_streaks(updated_at);

-- Personalized news storage
CREATE TABLE IF NOT EXISTS user_news (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source VARCHAR(50) NOT NULL,
    title TEXT NOT NULL,
    summary TEXT,
    url TEXT NOT NULL,
    related_symbols TEXT[] NOT NULL DEFAULT '{}',
    published_at TIMESTAMP WITH TIME ZONE NOT NULL,
    is_read BOOLEAN NOT NULL DEFAULT false,
    relevance_score DECIMAL(3, 2) CHECK (relevance_score >= 0 AND relevance_score <= 1),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_news_user_id ON user_news(user_id);
CREATE INDEX idx_user_news_is_read ON user_news(is_read);
CREATE INDEX idx_user_news_published_at ON user_news(published_at);
CREATE INDEX idx_user_news_relevance_score ON user_news(relevance_score);

-- AI chat sessions
CREATE TABLE IF NOT EXISTS ai_chat_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_message_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_chat_sessions_user_id ON ai_chat_sessions(user_id);
CREATE INDEX idx_ai_chat_sessions_last_message_at ON ai_chat_sessions(last_message_at);

-- AI chat messages
CREATE TABLE IF NOT EXISTS ai_chat_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID NOT NULL REFERENCES ai_chat_sessions(id) ON DELETE CASCADE,
    role VARCHAR(10) NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ai_chat_messages_session_id ON ai_chat_messages(session_id);
CREATE INDEX idx_ai_chat_messages_created_at ON ai_chat_messages(created_at);

-- Basket recommendations
CREATE TABLE IF NOT EXISTS basket_recommendations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recommended_basket_id UUID NOT NULL REFERENCES baskets(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    expected_return DECIMAL(5, 2),
    risk_change VARCHAR(20),
    confidence_score DECIMAL(3, 2) CHECK (confidence_score >= 0 AND confidence_score <= 1),
    is_applied BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX idx_basket_recommendations_user_id ON basket_recommendations(user_id);
CREATE INDEX idx_basket_recommendations_is_applied ON basket_recommendations(is_applied);
CREATE INDEX idx_basket_recommendations_expires_at ON basket_recommendations(expires_at);

-- Rebalance previews
CREATE TABLE IF NOT EXISTS rebalance_previews (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_allocation JSONB NOT NULL,
    trades_preview JSONB NOT NULL,
    expected_fees DECIMAL(36, 18) NOT NULL DEFAULT 0,
    expected_tax_impact DECIMAL(36, 18) NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'executed', 'expired', 'cancelled')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX idx_rebalance_previews_user_id ON rebalance_previews(user_id);
CREATE INDEX idx_rebalance_previews_status ON rebalance_previews(status);
CREATE INDEX idx_rebalance_previews_expires_at ON rebalance_previews(expires_at);

-- Enhanced ai_summaries table with new columns
-- Check if columns already exist before adding them
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'ai_summaries' AND column_name = 'summary_type'
    ) THEN
        ALTER TABLE ai_summaries ADD COLUMN summary_type VARCHAR(20) DEFAULT 'weekly';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'ai_summaries' AND column_name = 'cards_json'
    ) THEN
        ALTER TABLE ai_summaries ADD COLUMN cards_json JSONB;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'ai_summaries' AND column_name = 'insights_json'
    ) THEN
        ALTER TABLE ai_summaries ADD COLUMN insights_json JSONB;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_ai_summaries_summary_type ON ai_summaries(summary_type);

-- Add trigger to update investment_streaks.updated_at
CREATE TRIGGER update_investment_streaks_updated_at 
    BEFORE UPDATE ON investment_streaks 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

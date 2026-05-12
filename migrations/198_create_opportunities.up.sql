-- Opportunity sources (Superteam Earn, future: Dework, Layer3, etc.)
CREATE TABLE IF NOT EXISTS opportunity_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key_ref TEXT, -- reference to secret store, not the actual key
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Normalized opportunity listings
CREATE TABLE IF NOT EXISTS opportunity_listings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id UUID NOT NULL REFERENCES opportunity_sources(id),
    external_id TEXT NOT NULL,
    slug TEXT,
    title TEXT NOT NULL,
    description TEXT,
    type TEXT NOT NULL, -- bounty, project, hackathon
    skills TEXT[] DEFAULT '{}',
    reward_amount NUMERIC(12,2),
    reward_currency TEXT DEFAULT 'USDC',
    deadline TIMESTAMPTZ,
    sponsor TEXT,
    url TEXT NOT NULL,
    remote BOOLEAN DEFAULT true,
    status TEXT NOT NULL DEFAULT 'open', -- open, closed, expired
    agent_access TEXT, -- AGENT_ALLOWED, AGENT_ONLY, null
    raw_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_id, external_id)
);

-- User opportunity profiles (skills, preferences)
CREATE TABLE IF NOT EXISTS user_opportunity_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    skills TEXT[] DEFAULT '{}',
    interests TEXT[] DEFAULT '{}',
    preferred_types TEXT[] DEFAULT '{}', -- bounty, project, hackathon
    hours_per_week INTEGER DEFAULT 5,
    min_reward NUMERIC(12,2) DEFAULT 0,
    preferred_currency TEXT DEFAULT 'USDC',
    bio TEXT,
    portfolio_links TEXT[] DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Per-user opportunity matches (scored recommendations)
CREATE TABLE IF NOT EXISTS opportunity_matches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES opportunity_listings(id) ON DELETE CASCADE,
    fit_score NUMERIC(5,2) NOT NULL DEFAULT 0, -- 0-100
    week_start DATE NOT NULL,
    rank INTEGER, -- 1, 2, 3 for weekly picks
    explanation TEXT,
    status TEXT NOT NULL DEFAULT 'recommended', -- recommended, saved, hidden, applied
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, listing_id, week_start)
);

-- User feedback on opportunities
CREATE TABLE IF NOT EXISTS opportunity_feedback (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    listing_id UUID NOT NULL REFERENCES opportunity_listings(id) ON DELETE CASCADE,
    action TEXT NOT NULL, -- saved, hidden, applied, not_interested, won, lost
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_opportunity_listings_status ON opportunity_listings(status) WHERE status = 'open';
CREATE INDEX idx_opportunity_listings_deadline ON opportunity_listings(deadline) WHERE status = 'open';
CREATE INDEX idx_opportunity_matches_user_week ON opportunity_matches(user_id, week_start);
CREATE INDEX idx_opportunity_feedback_user ON opportunity_feedback(user_id);

-- Seed Superteam Earn as first source
INSERT INTO opportunity_sources (name, base_url, enabled)
VALUES ('superteam_earn', 'https://superteam.fun', true)
ON CONFLICT (name) DO NOTHING;

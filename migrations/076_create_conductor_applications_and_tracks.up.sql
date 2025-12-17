-- Conductor Applications: Users applying to become conductors
CREATE TABLE IF NOT EXISTS conductor_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    display_name VARCHAR(128) NOT NULL,
    bio TEXT NOT NULL,
    investment_strategy TEXT NOT NULL,
    experience TEXT NOT NULL,
    social_links JSONB,
    status VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending, approved, rejected
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP WITH TIME ZONE,
    rejection_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Tracks: Curated portfolio strategies created by conductors
CREATE TABLE IF NOT EXISTS tracks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conductor_id UUID NOT NULL REFERENCES conductors(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL,
    risk_level VARCHAR(16) NOT NULL, -- low, medium, high
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    followers_count INTEGER NOT NULL DEFAULT 0,
    total_return DECIMAL(10, 6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Track Allocations: Asset allocations within a track
CREATE TABLE IF NOT EXISTS track_allocations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    asset_ticker VARCHAR(16) NOT NULL,
    asset_name VARCHAR(128) NOT NULL,
    target_weight DECIMAL(5, 2) NOT NULL, -- Percentage 0-100
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT check_weight_range CHECK (target_weight >= 0 AND target_weight <= 100)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_conductor_applications_user ON conductor_applications(user_id);
CREATE INDEX IF NOT EXISTS idx_conductor_applications_status ON conductor_applications(status) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_tracks_conductor ON tracks(conductor_id);
CREATE INDEX IF NOT EXISTS idx_tracks_active ON tracks(is_active) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_track_allocations_track ON track_allocations(track_id);

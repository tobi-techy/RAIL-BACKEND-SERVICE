-- Add rail_tag column to users table for P2P transfers
-- RailTag is like Cash App's $cashtag - a unique username for sending money

ALTER TABLE users ADD COLUMN rail_tag VARCHAR(30) UNIQUE;

-- Index for fast lookups
CREATE INDEX idx_users_rail_tag ON users(rail_tag) WHERE rail_tag IS NOT NULL;

-- Constraint: lowercase alphanumeric only, 3-30 chars
ALTER TABLE users ADD CONSTRAINT chk_rail_tag_format 
    CHECK (rail_tag IS NULL OR rail_tag ~ '^[a-z0-9]{3,30}$');

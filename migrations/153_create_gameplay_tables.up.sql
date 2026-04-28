-- Gameplay, Subscription, and Leaderboard tables

-- Streak tracking
CREATE TABLE IF NOT EXISTS user_streaks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    streak_type VARCHAR(20) NOT NULL CHECK (streak_type IN ('deposit', 'no_spend', 'stash_growth', 'roundup')),
    current_count INT NOT NULL DEFAULT 0,
    longest_count INT NOT NULL DEFAULT 0,
    last_activity_at TIMESTAMP WITH TIME ZONE,
    started_at TIMESTAMP WITH TIME ZONE,
    broken_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, streak_type)
);
CREATE INDEX idx_user_streaks_user_id ON user_streaks(user_id);

-- XP and levels
CREATE TABLE IF NOT EXISTS user_xp (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    total_xp BIGINT NOT NULL DEFAULT 0,
    current_level INT NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS xp_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(50) NOT NULL,
    xp_amount INT NOT NULL,
    source_id UUID,
    description VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_xp_events_user_created ON xp_events(user_id, created_at DESC);

-- Challenges
CREATE TABLE IF NOT EXISTS challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_type VARCHAR(20) NOT NULL CHECK (challenge_type IN ('weekly', 'monthly', 'onetime')),
    title VARCHAR(100) NOT NULL,
    description VARCHAR(500) NOT NULL,
    target_metric VARCHAR(50) NOT NULL,
    target_value DECIMAL(20,6) NOT NULL,
    xp_reward INT NOT NULL,
    active_from TIMESTAMP WITH TIME ZONE,
    active_until TIMESTAMP WITH TIME ZONE,
    is_active BOOLEAN NOT NULL DEFAULT true,
    pro_only BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_id UUID NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
    progress DECIMAL(20,6) NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'expired')),
    started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, challenge_id)
);
CREATE INDEX idx_user_challenges_user_status ON user_challenges(user_id, status);

-- Achievements / Badges
CREATE TABLE IF NOT EXISTS achievements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(50) NOT NULL UNIQUE,
    description VARCHAR(255) NOT NULL,
    condition_type VARCHAR(50) NOT NULL,
    condition_value DECIMAL(20,6) NOT NULL DEFAULT 0,
    rarity VARCHAR(20) NOT NULL CHECK (rarity IN ('common', 'uncommon', 'rare', 'epic', 'legendary')),
    icon VARCHAR(50) NOT NULL DEFAULT 'badge',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_achievements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_id UUID NOT NULL REFERENCES achievements(id) ON DELETE CASCADE,
    unlocked_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, achievement_id)
);
CREATE INDEX idx_user_achievements_user ON user_achievements(user_id);

-- Subscriptions
CREATE TABLE IF NOT EXISTS subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    plan VARCHAR(20) NOT NULL DEFAULT 'pro',
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired', 'past_due')),
    started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    current_period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    current_period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    cancelled_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS subscription_charges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(20,6) NOT NULL,
    ledger_transaction_id UUID,
    status VARCHAR(30) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed', 'insufficient_funds')),
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    charged_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_subscription_charges_sub ON subscription_charges(subscription_id);

-- Leaderboard snapshots (schema now, feature Phase 4)
CREATE TABLE IF NOT EXISTS leaderboard_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    leaderboard_type VARCHAR(30) NOT NULL,
    score BIGINT NOT NULL DEFAULT 0,
    rank INT,
    snapshot_date DATE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_leaderboard_type_date_score ON leaderboard_snapshots(leaderboard_type, snapshot_date, score DESC);

-- Seed achievements
INSERT INTO achievements (name, description, condition_type, condition_value, rarity, icon) VALUES
    ('First Blood', 'Made your first deposit', 'first_deposit', 1, 'common', 'deposit'),
    ('Centurion', 'Reached $100 in stash', 'stash_balance', 100, 'common', 'shield'),
    ('Streak Lord', '30-day deposit streak', 'deposit_streak', 30, 'rare', 'flame'),
    ('Grand', '$1,000 total balance', 'total_balance', 1000, 'uncommon', 'star'),
    ('Round-Up King', '100 round-ups triggered', 'roundup_count', 100, 'uncommon', 'crown'),
    ('Diamond Hands', 'No stash withdrawal for 90 days', 'no_stash_withdrawal_days', 90, 'rare', 'diamond'),
    ('Five Figure Club', '$10,000 total balance', 'total_balance', 10000, 'epic', 'trophy'),
    ('Year One', 'Active for 365 days', 'account_age_days', 365, 'rare', 'calendar'),
    ('Recruiter', 'Referred 5 friends who deposited', 'referral_count', 5, 'rare', 'users'),
    ('OG', 'Among first 1,000 users', 'early_adopter', 1000, 'legendary', 'gem')
ON CONFLICT (name) DO NOTHING;

-- Seed onboarding challenges
INSERT INTO challenges (challenge_type, title, description, target_metric, target_value, xp_reward, is_active, pro_only) VALUES
    ('onetime', 'First Deposit', 'Make your first deposit to Rail', 'deposit_count', 1, 100, true, false),
    ('onetime', 'Card Activated', 'Make your first card transaction', 'card_tx_count', 1, 50, true, false),
    ('onetime', 'Round-Up Enabled', 'Enable round-ups on your account', 'roundup_enabled', 1, 25, true, false),
    ('onetime', 'Invite a Friend', 'Refer a friend who makes a deposit', 'referral_count', 1, 200, true, false);

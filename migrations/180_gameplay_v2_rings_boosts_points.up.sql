-- Gameplay V2: Rings, Boosts, Rail Points, Grace Days, Weekly Recaps
-- + expand streak types, seed new badges & quests

-- 1. Expand streak_type constraint to allow new types
ALTER TABLE user_streaks DROP CONSTRAINT IF EXISTS user_streaks_streak_type_check;
ALTER TABLE user_streaks ADD CONSTRAINT user_streaks_streak_type_check
    CHECK (streak_type IN ('deposit', 'no_spend', 'stash_growth', 'roundup',
                           'no_panic_withdrawal', 'weekly_goal', 'emergency_fund_growth'));

-- 2. Daily Rings (Apple Fitness style)
CREATE TABLE IF NOT EXISTS daily_rings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ring_date DATE NOT NULL,
    spend_target DECIMAL(20,6) NOT NULL DEFAULT 0,
    spend_actual DECIMAL(20,6) NOT NULL DEFAULT 0,
    save_target DECIMAL(20,6) NOT NULL DEFAULT 0,
    save_actual DECIMAL(20,6) NOT NULL DEFAULT 0,
    grow_target DECIMAL(20,6) NOT NULL DEFAULT 0,
    grow_actual DECIMAL(20,6) NOT NULL DEFAULT 0,
    spend_closed BOOLEAN NOT NULL DEFAULT false,
    save_closed BOOLEAN NOT NULL DEFAULT false,
    grow_closed BOOLEAN NOT NULL DEFAULT false,
    all_closed BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, ring_date)
);
CREATE INDEX idx_daily_rings_user_date ON daily_rings(user_id, ring_date DESC);

-- 3. Boosts (Cash App style card-linked rewards)
CREATE TABLE IF NOT EXISTS boosts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    description VARCHAR(500) NOT NULL,
    boost_type VARCHAR(30) NOT NULL CHECK (boost_type IN ('cashback_stash', 'points_multiplier', 'set_aside', 'no_spend_bonus')),
    category VARCHAR(50),
    reward_value DECIMAL(10,4) NOT NULL,
    reward_unit VARCHAR(20) NOT NULL DEFAULT 'percent' CHECK (reward_unit IN ('percent', 'fixed', 'points')),
    condition_type VARCHAR(50) NOT NULL DEFAULT 'any_spend',
    condition_value DECIMAL(20,6) NOT NULL DEFAULT 0,
    duration_days INT NOT NULL DEFAULT 7,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_boosts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    boost_id UUID NOT NULL REFERENCES boosts(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'expired')),
    activated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    progress DECIMAL(20,6) NOT NULL DEFAULT 0,
    reward_earned DECIMAL(20,6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_user_boosts_user_status ON user_boosts(user_id, status);
-- Only one active boost per user at a time
CREATE UNIQUE INDEX idx_user_boosts_one_active ON user_boosts(user_id) WHERE status = 'active';

-- 4. Rail Points (Starbucks style reward currency)
CREATE TABLE IF NOT EXISTS rail_points (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    balance BIGINT NOT NULL DEFAULT 0,
    lifetime_earned BIGINT NOT NULL DEFAULT 0,
    lifetime_spent BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rail_point_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(30) NOT NULL CHECK (event_type IN ('earn', 'spend', 'expire', 'bonus')),
    amount BIGINT NOT NULL,
    source VARCHAR(50) NOT NULL,
    source_id UUID,
    description VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rail_point_events_user ON rail_point_events(user_id, created_at DESC);

-- 5. Grace Days (Duolingo streak freeze)
CREATE TABLE IF NOT EXISTS grace_days (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    remaining INT NOT NULL DEFAULT 0,
    used_total INT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id)
);

-- 6. Weekly Recaps (Nike Run Club style)
CREATE TABLE IF NOT EXISTS weekly_recaps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    week_start DATE NOT NULL,
    week_end DATE NOT NULL,
    total_deposited DECIMAL(20,6) NOT NULL DEFAULT 0,
    total_spent DECIMAL(20,6) NOT NULL DEFAULT 0,
    total_saved DECIMAL(20,6) NOT NULL DEFAULT 0,
    total_grown DECIMAL(20,6) NOT NULL DEFAULT 0,
    spend_vs_last_week_pct DECIMAL(10,2) NOT NULL DEFAULT 0,
    rings_closed INT NOT NULL DEFAULT 0,
    streak_days INT NOT NULL DEFAULT 0,
    points_earned BIGINT NOT NULL DEFAULT 0,
    badges_earned INT NOT NULL DEFAULT 0,
    coaching_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, week_start)
);
CREATE INDEX idx_weekly_recaps_user ON weekly_recaps(user_id, week_start DESC);

-- 7. Seed new achievements (badges from design doc)
INSERT INTO achievements (name, description, condition_type, condition_value, rarity, icon) VALUES
    ('7-Day Control', 'Stayed within spend balance for 7 days straight', 'spend_control_days', 7, 'uncommon', 'shield'),
    ('Quiet ₦10K', 'First ₦10,000 set aside quietly', 'stash_balance_ngn', 10000, 'uncommon', 'quiet'),
    ('No Panic Week', 'No early withdrawal for 7 days', 'no_panic_withdrawal_days', 7, 'rare', 'calm'),
    ('Salary Split', 'First income automatically handled by Rail', 'salary_split_count', 1, 'common', 'split'),
    ('Comeback', 'Recovered after overspending — got back on Rail', 'comeback_count', 1, 'rare', 'comeback'),
    ('Ring Closer', 'Closed all 3 rings in a single day', 'rings_all_closed_days', 1, 'uncommon', 'rings'),
    ('Week Warrior', 'Closed all rings every day for a full week', 'rings_all_closed_days', 7, 'rare', 'warrior'),
    ('Point Collector', 'Earned 1,000 Rail Points', 'lifetime_points', 1000, 'uncommon', 'points'),
    ('Boost Master', 'Completed 5 boosts', 'boosts_completed', 5, 'rare', 'boost'),
    ('Grace Saver', 'Used a Grace Day to protect a streak', 'grace_days_used', 1, 'common', 'freeze')
ON CONFLICT (name) DO NOTHING;

-- 8. Seed new challenges (quests from design doc)
INSERT INTO challenges (challenge_type, title, description, target_metric, target_value, xp_reward, is_active, pro_only) VALUES
    ('weekly', 'Stay on Rail', 'Stay under your spend rail for 5 days', 'spend_control_days', 5, 60, true, false),
    ('weekly', 'Emergency Builder', 'Build your emergency fund by ₦1,000', 'emergency_fund_growth', 1000, 50, true, false),
    ('weekly', 'Recap Reader', 'Read your weekly money recap', 'recap_read', 1, 20, true, false),
    ('weekly', 'Ring Chaser', 'Close all 3 rings at least 3 days this week', 'rings_all_closed_days', 3, 70, true, false),
    ('weekly', 'Point Hunter', 'Earn 100 Rail Points this week', 'points_earned_weekly', 100, 40, true, false),
    ('monthly', 'Boost Finisher', 'Complete 2 boosts this month', 'boosts_completed_monthly', 2, 100, true, false),
    ('monthly', 'Streak Guardian', 'Maintain any streak for 30 days', 'max_streak_days', 30, 200, true, false);

-- 9. Seed initial boosts
INSERT INTO boosts (name, description, boost_type, category, reward_value, reward_unit, condition_type, condition_value, duration_days) VALUES
    ('Food Saver', '2% back into Stash on food spending this week', 'cashback_stash', 'food', 2.0, 'percent', 'category_spend', 5000, 7),
    ('Transport Boost', '₦200 set aside after 5 transport rides', 'set_aside', 'transport', 200, 'fixed', 'category_count', 5, 7),
    ('No-Spend Hero', 'Complete 2 low-spend days, earn 50 bonus points', 'no_spend_bonus', NULL, 50, 'points', 'low_spend_days', 2, 7),
    ('Card Warrior', 'Use Rail card 5 times, earn 2x points', 'points_multiplier', NULL, 2.0, 'percent', 'card_tx_count', 5, 7),
    ('Stash Sprint', 'Add ₦5,000 to stash this week, earn 100 bonus points', 'no_spend_bonus', NULL, 100, 'points', 'stash_growth', 5000, 7);

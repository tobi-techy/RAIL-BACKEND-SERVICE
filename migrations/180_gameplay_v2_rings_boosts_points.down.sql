-- Delete seed data from existing tables first (before dropping new tables)
DELETE FROM achievements WHERE name IN ('7-Day Control', 'Quiet ₦10K', 'No Panic Week', 'Salary Split', 'Comeback', 'Ring Closer', 'Week Warrior', 'Point Collector', 'Boost Master', 'Grace Saver');
DELETE FROM challenges WHERE title IN ('Stay on Rail', 'Emergency Builder', 'Recap Reader', 'Ring Chaser', 'Point Hunter', 'Boost Finisher', 'Streak Guardian');

-- Drop new tables
DROP TABLE IF EXISTS weekly_recaps;
DROP TABLE IF EXISTS grace_days;
DROP TABLE IF EXISTS rail_point_events;
DROP TABLE IF EXISTS rail_points;
DROP TABLE IF EXISTS user_boosts;
DROP TABLE IF EXISTS boosts;
DROP TABLE IF EXISTS daily_rings;

-- Restore original streak_type constraint
ALTER TABLE user_streaks DROP CONSTRAINT IF EXISTS user_streaks_streak_type_check;
ALTER TABLE user_streaks ADD CONSTRAINT user_streaks_streak_type_check
    CHECK (streak_type IN ('deposit', 'no_spend', 'stash_growth', 'roundup'));

-- Seed weekly and monthly challenge templates for Pro users

INSERT INTO challenges (challenge_type, title, description, target_metric, target_value, xp_reward, is_active, pro_only) VALUES
    -- Weekly challenges (rotate every Monday)
    ('weekly', 'Triple Deposit', 'Deposit 3 times this week', 'deposit_count', 3, 50, true, true),
    ('weekly', 'No-Spend Warrior', 'Keep a no-spend streak for 3 days', 'no_spend_days', 3, 40, true, true),
    ('weekly', 'Round-Up Rush', 'Round up at least 5 transactions', 'roundup_count', 5, 30, true, true),
    ('weekly', 'Daily Depositor', 'Deposit every day for 5 days', 'deposit_count', 5, 75, true, true),
    ('weekly', 'Stash Builder', 'Add $25 to your stash this week', 'stash_growth', 25, 60, true, true),
    -- Monthly challenges
    ('monthly', 'Stash Growth', 'Grow your stash by $50 this month', 'stash_growth', 50, 150, true, true),
    ('monthly', 'Consistency King', 'Deposit every week this month', 'weekly_deposit_count', 4, 200, true, true),
    ('monthly', 'Milestone Hunter', 'Hit a new balance milestone', 'milestone_hit', 1, 100, true, true),
    ('monthly', 'Round-Up Master', 'Trigger 20 round-ups this month', 'roundup_count', 20, 120, true, true);

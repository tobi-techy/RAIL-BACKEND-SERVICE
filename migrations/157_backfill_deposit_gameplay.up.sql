-- Backfill XP and streaks for confirmed deposits that were never processed
-- through gameplay hooks (Bridge VA deposits before the fix).

-- Step 1: Award XP for deposits missing xp_events
-- XP scale: <$100 = 10 XP, $100-$499 = 30 XP, $500+ = 50 XP
INSERT INTO xp_events (user_id, event_type, xp_amount, source_id, description, created_at)
SELECT
    d.user_id,
    'deposit',
    CASE
        WHEN d.amount >= 500 THEN 50
        WHEN d.amount >= 100 THEN 30
        ELSE 10
    END,
    d.id,
    CASE
        WHEN d.amount >= 500 THEN '+50 XP: deposit (backfill)'
        WHEN d.amount >= 100 THEN '+30 XP: deposit (backfill)'
        ELSE '+10 XP: deposit (backfill)'
    END,
    d.confirmed_at
FROM deposits d
WHERE d.status = 'confirmed'
  AND d.confirmed_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM xp_events xe
      WHERE xe.source_id = d.id AND xe.event_type = 'deposit'
  );

-- Step 2: Update user_xp totals from the backfilled events
INSERT INTO user_xp (user_id, total_xp, current_level, created_at, updated_at)
SELECT
    xe.user_id,
    SUM(xe.xp_amount),
    CASE
        WHEN SUM(xe.xp_amount) >= 50000 THEN 10
        WHEN SUM(xe.xp_amount) >= 25000 THEN 9
        WHEN SUM(xe.xp_amount) >= 12000 THEN 8
        WHEN SUM(xe.xp_amount) >= 6000  THEN 7
        WHEN SUM(xe.xp_amount) >= 3000  THEN 6
        WHEN SUM(xe.xp_amount) >= 1500  THEN 5
        WHEN SUM(xe.xp_amount) >= 700   THEN 4
        WHEN SUM(xe.xp_amount) >= 300   THEN 3
        WHEN SUM(xe.xp_amount) >= 100   THEN 2
        ELSE 1
    END,
    NOW(),
    NOW()
FROM xp_events xe
GROUP BY xe.user_id
ON CONFLICT (user_id) DO UPDATE SET
    total_xp = (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id),
    current_level = CASE
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 50000 THEN 10
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 25000 THEN 9
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 12000 THEN 8
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 6000  THEN 7
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 3000  THEN 6
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 1500  THEN 5
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 700   THEN 4
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 300   THEN 3
        WHEN (SELECT COALESCE(SUM(xp_amount), 0) FROM xp_events WHERE user_id = EXCLUDED.user_id) >= 100   THEN 2
        ELSE 1
    END,
    updated_at = NOW();

-- Step 3: Upsert deposit streaks using the most recent deposit per user
-- Sets current_count to number of distinct deposit days within the last 7 days
-- (matching the StreakResetDays[deposit] = 7 rule)
INSERT INTO user_streaks (user_id, streak_type, current_count, longest_count, last_activity_at, started_at, created_at, updated_at)
SELECT
    d.user_id,
    'deposit',
    COUNT(DISTINCT d.confirmed_at::date) FILTER (WHERE d.confirmed_at >= NOW() - INTERVAL '7 days'),
    COUNT(DISTINCT d.confirmed_at::date),
    MAX(d.confirmed_at),
    MIN(d.confirmed_at),
    NOW(),
    NOW()
FROM deposits d
WHERE d.status = 'confirmed' AND d.confirmed_at IS NOT NULL
GROUP BY d.user_id
ON CONFLICT (user_id, streak_type) DO UPDATE SET
    current_count = GREATEST(user_streaks.current_count, EXCLUDED.current_count),
    longest_count = GREATEST(user_streaks.longest_count, EXCLUDED.longest_count),
    last_activity_at = GREATEST(user_streaks.last_activity_at, EXCLUDED.last_activity_at),
    updated_at = NOW();

-- Step 4: Update deposit_count challenge progress
UPDATE user_challenges uc
SET progress = GREATEST(uc.progress, sub.deposit_count),
    status = CASE
        WHEN GREATEST(uc.progress, sub.deposit_count) >= c.target_value THEN 'completed'
        ELSE uc.status
    END,
    completed_at = CASE
        WHEN GREATEST(uc.progress, sub.deposit_count) >= c.target_value AND uc.completed_at IS NULL THEN NOW()
        ELSE uc.completed_at
    END
FROM (
    SELECT user_id, COUNT(*)::decimal AS deposit_count
    FROM deposits
    WHERE status = 'confirmed'
    GROUP BY user_id
) sub,
challenges c
WHERE uc.user_id = sub.user_id
  AND c.id = uc.challenge_id
  AND c.target_metric = 'deposit_count';

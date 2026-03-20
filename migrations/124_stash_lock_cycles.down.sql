-- WARNING: This permanently deletes all stash lock cycle data.
-- All 90-day lock tracking and withdrawal window state will be lost.
-- Only run this in development. In production, disable the feature via config instead.
DROP TABLE IF EXISTS stash_lock_cycles;

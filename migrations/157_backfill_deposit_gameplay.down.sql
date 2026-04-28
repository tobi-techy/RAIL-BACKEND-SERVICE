-- Backfill is additive and idempotent; no rollback needed.
-- To undo, delete xp_events with description LIKE '%backfill%'
DELETE FROM xp_events WHERE description LIKE '%(backfill)%';

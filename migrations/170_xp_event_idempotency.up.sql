-- Add unique constraint to prevent duplicate XP awards for the same event
CREATE UNIQUE INDEX IF NOT EXISTS idx_xp_events_user_event_source
    ON xp_events (user_id, event_type, source_id)
    WHERE source_id IS NOT NULL;

-- Add window_notified status to prevent duplicate open-window notifications.
ALTER TABLE stash_lock_cycles
    DROP CONSTRAINT IF EXISTS stash_lock_cycles_status_check,
    ADD CONSTRAINT stash_lock_cycles_status_check
        CHECK (status IN ('locked', 'window_open', 'window_notified', 'withdrawn', 'relocked'));

-- Partial index for GetUnlockedPending: status='locked' AND lock_end < now.
CREATE INDEX IF NOT EXISTS idx_stash_lock_cycles_status_lock_end
    ON stash_lock_cycles(status, lock_end)
    WHERE status = 'locked';

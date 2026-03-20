DROP INDEX IF EXISTS idx_stash_lock_cycles_status_lock_end;

ALTER TABLE stash_lock_cycles
    DROP CONSTRAINT IF EXISTS stash_lock_cycles_status_check,
    ADD CONSTRAINT stash_lock_cycles_status_check
        CHECK (status IN ('locked', 'window_open', 'withdrawn', 'relocked'));

-- Tracks the lock/window cycle for each stash deposit.
-- Each deposit starts a 90-day lock. After lock expires, a 7-day window opens.
-- If window expires without withdrawal, a new 90-day lock starts.
CREATE TABLE stash_lock_cycles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    deposit_id      UUID NOT NULL,                        -- references the originating deposit
    amount          NUMERIC(20,6) NOT NULL,
    lock_start      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lock_end        TIMESTAMPTZ NOT NULL,                 -- lock_start + 90 days
    window_end      TIMESTAMPTZ NOT NULL,                 -- lock_end + 7 days
    status          TEXT NOT NULL DEFAULT 'locked',       -- locked | window_open | withdrawn | relocked
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_slc_user_status ON stash_lock_cycles(user_id, status);
CREATE INDEX idx_slc_window_end  ON stash_lock_cycles(window_end) WHERE status = 'window_open';

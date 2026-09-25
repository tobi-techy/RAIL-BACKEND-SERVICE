-- Down is a no-op schema restore (data cannot be recovered).
-- Recreate the empty table so a rollback does not break code expecting it.
CREATE TABLE IF NOT EXISTS confirmation_device_keys (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    spki        BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_confirmation_device_keys_user
    ON confirmation_device_keys (user_id);

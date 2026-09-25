-- Confirmation device approval keys (Face ID-bound, Secure Enclave).
--
-- One row per iPhone enrolled for live confirmation cards. The private half
-- never leaves the device Secure Enclave (biometryCurrentSet access control);
-- this table holds the P-256 SPKI the server verifies approve signatures
-- against. Trust-on-first-use enrollment is audited in the application log
-- (confirmation transition audit, biometric=enrolled).
CREATE TABLE IF NOT EXISTS confirmation_device_keys (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    spki        BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_confirmation_device_keys_user
    ON confirmation_device_keys (user_id);

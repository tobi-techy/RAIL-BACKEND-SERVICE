-- Messaging opt-outs ("STOP").
--
-- Someone who texts STOP must not be messaged again. This is durable rather
-- than Redis-only on purpose: an opt-out that a cache flush can lose is not an
-- opt-out, and messaging providers require it to be honoured. The row is the
-- record of the request.
--
-- A resume ("START") stamps resumed_at rather than deleting the row, so the
-- history of who asked to stop — and when — survives for compliance evidence.
-- "Currently opted out" is therefore resumed_at IS NULL.

CREATE TABLE IF NOT EXISTS platform_optouts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    platform         VARCHAR(32)  NOT NULL,
    platform_user_id VARCHAR(255) NOT NULL,
    reason           TEXT,
    resumed_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_platform_optout UNIQUE (platform, platform_user_id)
);

-- The suppression check runs on every outbound message, so it must be a cheap
-- index hit. Partial on resumed_at IS NULL because that is the only state the
-- hot path asks about.
CREATE INDEX IF NOT EXISTS idx_platform_optouts_active
    ON platform_optouts (platform, platform_user_id)
    WHERE resumed_at IS NULL;

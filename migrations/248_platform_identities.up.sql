CREATE TABLE platform_identities (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform            VARCHAR(32) NOT NULL,  -- 'imessage', 'whatsapp', 'telegram'
    platform_user_id    VARCHAR(255) NOT NULL, -- the external user identifier on the platform (phone number, platform user ID, etc.)
    platform_username   VARCHAR(255),           -- optional display name from the platform
    handshake_token_hash VARCHAR(64),           -- SHA-256 hash of the current pending handshake token (NULL if no pending link)
    handshake_expires_at TIMESTAMPTZ,           -- when the pending handshake expires
    linked_at           TIMESTAMPTZ,            -- when the user completed the handshake (NULL = pending)
    last_used_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_platform_user UNIQUE (platform, platform_user_id),
    CONSTRAINT uq_user_platform UNIQUE (user_id, platform)
);

CREATE INDEX idx_platform_identities_user ON platform_identities (user_id);
CREATE INDEX idx_platform_identities_platform_user ON platform_identities (platform, platform_user_id);

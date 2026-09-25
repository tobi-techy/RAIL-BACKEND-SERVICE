-- Live confirmation cards (Face ID / passkey approvals for money actions).
--
-- One row per card. The row is the cross-replica source of truth for the
-- single-use token: token_used flips in the same write as the first state
-- transition, and execute_key is unique so a crash-retry or a second replica
-- can never execute twice. Short-lived ceremony sessions stay in memory;
-- only card truth lives here.
CREATE TABLE IF NOT EXISTS confirmations (
    id             UUID PRIMARY KEY,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action         TEXT NOT NULL,
    state          TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    subtitle       TEXT NOT NULL DEFAULT '',
    amount         TEXT NOT NULL DEFAULT '',
    asset          TEXT NOT NULL DEFAULT '',
    destination    TEXT NOT NULL DEFAULT '',
    fee            TEXT NOT NULL DEFAULT '',
    risk_line      TEXT NOT NULL DEFAULT '',
    payload        JSONB NOT NULL DEFAULT '{}',
    execute_key    TEXT NOT NULL UNIQUE,
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    result_summary TEXT NOT NULL DEFAULT '',
    assurance      TEXT NOT NULL DEFAULT '',
    token_used     BOOLEAN NOT NULL DEFAULT FALSE,
    card_edit_failed BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_confirmations_user
    ON confirmations (user_id);
CREATE INDEX IF NOT EXISTS idx_confirmations_expires
    ON confirmations (expires_at) WHERE token_used = FALSE;

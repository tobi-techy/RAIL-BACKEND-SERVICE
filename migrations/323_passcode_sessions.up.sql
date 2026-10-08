-- Short-lived passcode step-up sessions.
--
-- The X-Passcode-Session token that authorises money movement used to live only
-- in Redis. That made a Redis outage an outage of the withdrawal path: passcode
-- verification failed while creating the session, so the client never received a
-- token, and RequirePasscodeSession rejected the request with 403. On 2026-10-08
-- an Upstash quota breach produced exactly that — a user could not withdraw.
--
-- Postgres is where login sessions already live, and these rows are extremely
-- low-volume (one per passcode step-up, 10 minute TTL), so the money path no
-- longer depends on Redis being reachable.
CREATE TABLE IF NOT EXISTS passcode_sessions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    issued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_passcode_sessions_expires_at ON passcode_sessions (expires_at);
CREATE INDEX IF NOT EXISTS idx_passcode_sessions_user_id ON passcode_sessions (user_id);

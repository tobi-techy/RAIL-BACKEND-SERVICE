-- Vault tiers, contribution skips and health (Premium Global Dollar Retirement Vault).
--
-- vault_tiers pins each tier to the Rail-owned strategy the bootstrap resolved,
-- so tier files with unresolved ids fail closed instead of enrolling users into
-- a half-configured plan.
CREATE TABLE IF NOT EXISTS vault_tiers (
    tier                TEXT PRIMARY KEY CHECK (tier IN ('conservative_pension', 'balanced_wealth', 'bold_growth')),
    rail_strategy_id    UUID NOT NULL,
    glider_strategy_id  TEXT,
    version             INTEGER NOT NULL DEFAULT 1,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- vault_skips records automatic-saving takes the spendable floor refused. No
-- lot is opened for these rows; the hook writes the skip and moves on.
CREATE TABLE IF NOT EXISTS vault_skips (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vault_id            UUID NOT NULL REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    payment_id          UUID NOT NULL,
    reason              TEXT NOT NULL DEFAULT 'floor' CHECK (reason IN ('floor')),
    would_have_been_usd NUMERIC(36,18) NOT NULL DEFAULT 0,
    spendable_usd       NUMERIC(36,18) NOT NULL DEFAULT 0,
    floor_usd           NUMERIC(36,18) NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (vault_id, payment_id, reason)
);

CREATE INDEX IF NOT EXISTS idx_vault_skips_vault_created ON vault_skips(vault_id, created_at DESC);

-- vault_health is the ops-facing state of one vault. One row per vault,
-- upserted on every state change. Flags name the condition; user_action_needed
-- is true only when the user must act (unlock within 30 days).
CREATE TABLE IF NOT EXISTS vault_health (
    vault_id            UUID PRIMARY KEY REFERENCES retirement_vaults(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_funded_at      TIMESTAMPTZ,
    last_snapshot_at    TIMESTAMPTZ,
    ledger_usd          NUMERIC(36,18) NOT NULL DEFAULT 0,
    provider_usd        NUMERIC(36,18) NOT NULL DEFAULT 0,
    pending_ops         INTEGER NOT NULL DEFAULT 0,
    last_skip           TIMESTAMPTZ,
    last_error          TEXT NOT NULL DEFAULT '',
    last_error_at       TIMESTAMPTZ,
    flags               TEXT[] NOT NULL DEFAULT '{}',
    user_action_needed  BOOLEAN NOT NULL DEFAULT FALSE,
    checked_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- vault_enroll_failures: a failed plan open is observable by ops even though it
-- never creates a vault row (and so can never own a vault_health row, which is
-- keyed by vault). One row per user, carrying the most recent failure.
CREATE TABLE IF NOT EXISTS vault_enroll_failures (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_error     TEXT NOT NULL DEFAULT '',
    last_error_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tries          INTEGER NOT NULL DEFAULT 1
);

-- A pending vault row exists only so a failed open can clean itself up. It is
-- never a plan: reads filter it out, and the one-active-per-user index must let
-- a user with a dead pending row still open a real plan.
DROP INDEX IF EXISTS idx_retirement_vaults_one_active;
CREATE UNIQUE INDEX IF NOT EXISTS idx_retirement_vaults_one_active
    ON retirement_vaults(user_id) WHERE status = 'active';
ALTER TABLE retirement_vaults DROP CONSTRAINT IF EXISTS retirement_vaults_status_check;
ALTER TABLE retirement_vaults
    ADD CONSTRAINT retirement_vaults_status_check
    CHECK (status IN ('active', 'paused', 'closed', 'pending'));

-- A vault lot is opened at most once per payment. The hook is idempotent on the
-- payment's key, and the unique index makes the second writer lose the race
-- instead of double-funding.
CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_lots_vault_payment_key
    ON vault_contribution_lots(vault_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Withdrawals and penalties feed the activity view.
CREATE INDEX IF NOT EXISTS idx_vault_penalties_vault_created ON vault_penalty_events(vault_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_vault_auth_vault_created ON vault_withdrawal_authorizations(vault_id, created_at DESC);

-- Premium Global Dollar Retirement Vault: the locked-USD retirement account.
-- One active vault per user. Principal is reachable with friction; earnings are
-- locked until unlock_date (age target OR minimum lock, whichever is later).

CREATE TABLE IF NOT EXISTS retirement_vaults (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                  TEXT NOT NULL DEFAULT 'USD Retirement Plan',
    tier                  TEXT NOT NULL CHECK (tier IN ('conservative_pension', 'balanced_wealth', 'bold_growth')),
    status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'closed')),
    retirement_age        INTEGER NOT NULL DEFAULT 50 CHECK (retirement_age >= 50 AND retirement_age <= 90),
    min_lock_years        INTEGER NOT NULL DEFAULT 5 CHECK (min_lock_years >= 0),
    funded_at             TIMESTAMPTZ,
    unlock_date           TIMESTAMPTZ,
    auto_contribution_pct NUMERIC(6,5) NOT NULL DEFAULT 0 CHECK (auto_contribution_pct >= 0 AND auto_contribution_pct <= 1),
    glider_enrollment_id  UUID REFERENCES investment_enrollments(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One active vault per user.
CREATE UNIQUE INDEX IF NOT EXISTS idx_retirement_vaults_one_active
    ON retirement_vaults(user_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_retirement_vaults_user ON retirement_vaults(user_id);
CREATE INDEX IF NOT EXISTS idx_retirement_vaults_enrollment ON retirement_vaults(glider_enrollment_id);

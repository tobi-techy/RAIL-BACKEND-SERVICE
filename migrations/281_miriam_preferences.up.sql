-- User-editable Miriam discretion settings (briefing, quiet hours, cadence, autonomy).
CREATE TABLE IF NOT EXISTS miriam_preferences (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    briefing_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    briefing_hour     SMALLINT NOT NULL DEFAULT 9
        CHECK (briefing_hour BETWEEN 0 AND 23),
    timezone          TEXT,
    quiet_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    quiet_start       SMALLINT NOT NULL DEFAULT 22
        CHECK (quiet_start BETWEEN 0 AND 23),
    quiet_end         SMALLINT NOT NULL DEFAULT 7
        CHECK (quiet_end BETWEEN 0 AND 23),
    daily_cap         SMALLINT NOT NULL DEFAULT 6
        CHECK (daily_cap BETWEEN 0 AND 50),
    allow_briefings   BOOLEAN NOT NULL DEFAULT TRUE,
    allow_risk        BOOLEAN NOT NULL DEFAULT TRUE,
    allow_nudges      BOOLEAN NOT NULL DEFAULT TRUE,
    allow_followups   BOOLEAN NOT NULL DEFAULT TRUE,
    autonomy_level    TEXT NOT NULL DEFAULT 'suggest'
        CHECK (autonomy_level IN ('observe', 'suggest', 'act')),
    humor_roasting    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_miriam_preferences_updated
    ON miriam_preferences (updated_at DESC);

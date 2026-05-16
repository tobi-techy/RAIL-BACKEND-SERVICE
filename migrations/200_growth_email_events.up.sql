CREATE TABLE IF NOT EXISTS growth_email_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    campaign_key VARCHAR(120) NOT NULL,
    campaign VARCHAR(80) NOT NULL,
    subject TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('sent', 'failed')),
    error TEXT,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, campaign_key)
);

CREATE INDEX IF NOT EXISTS idx_growth_email_events_user_created
    ON growth_email_events (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_growth_email_events_campaign_status
    ON growth_email_events (campaign, status, created_at DESC);

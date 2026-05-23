CREATE TABLE IF NOT EXISTS user_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_name VARCHAR(80) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_events_user_created
    ON user_events(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_events_name_created
    ON user_events(event_name, created_at DESC);

CREATE TABLE IF NOT EXISTS user_lifecycle (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    lifecycle_stage VARCHAR(80) NOT NULL DEFAULT 'signed_up',
    current_segment VARCHAR(80) NOT NULL DEFAULT 'active',
    last_active_at TIMESTAMPTZ,
    kyc_started_at TIMESTAMPTZ,
    kyc_completed_at TIMESTAMPTZ,
    first_deposit_started_at TIMESTAMPTZ,
    first_deposit_at TIMESTAMPTZ,
    last_deposit_at TIMESTAMPTZ,
    miriam_last_used_at TIMESTAMPTZ,
    allocation_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    reactivation_score INTEGER NOT NULL DEFAULT 0,
    segment_assigned_at TIMESTAMPTZ,
    reactivated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_lifecycle_segment
    ON user_lifecycle(current_segment, segment_assigned_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_lifecycle_stage
    ON user_lifecycle(lifecycle_stage);
CREATE INDEX IF NOT EXISTS idx_user_lifecycle_last_active
    ON user_lifecycle(last_active_at DESC);

CREATE TABLE IF NOT EXISTS growth_segments (
    segment VARCHAR(80) PRIMARY KEY,
    description TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO growth_segments (segment, description) VALUES
    ('signup_no_kyc', 'Signed up but did not start or complete KYC after 24 hours'),
    ('kyc_abandoned', 'Started KYC but did not complete it'),
    ('kyc_no_deposit', 'Completed KYC but has not made a first deposit'),
    ('inactive_7_days', 'Activated user inactive for at least 7 days'),
    ('miriam_user_inactive', 'User has used Miriam before and is now inactive'),
    ('fully_churned', 'User inactive for at least 14 days'),
    ('active', 'Currently active or not eligible for recovery')
ON CONFLICT (segment) DO UPDATE SET description = EXCLUDED.description;

CREATE TABLE IF NOT EXISTS notification_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_key VARCHAR(120) NOT NULL UNIQUE,
    channel VARCHAR(40) NOT NULL CHECK (channel IN ('email', 'push', 'manual_whatsapp', 'in_app')),
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    cta TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(160) NOT NULL,
    segment VARCHAR(80) NOT NULL REFERENCES growth_segments(segment),
    channel VARCHAR(40) NOT NULL CHECK (channel IN ('email', 'push', 'manual_whatsapp', 'in_app')),
    template_key VARCHAR(120) NOT NULL REFERENCES notification_templates(template_key),
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    cta TEXT NOT NULL DEFAULT '',
    cooldown_days INTEGER NOT NULL DEFAULT 7,
    conversion_event VARCHAR(80) NOT NULL,
    from_email VARCHAR(255) NOT NULL DEFAULT '',
    from_name VARCHAR(255) NOT NULL DEFAULT '',
    reply_to VARCHAR(255) NOT NULL DEFAULT '',
    priority INTEGER NOT NULL DEFAULT 100,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(segment, channel, template_key)
);

CREATE INDEX IF NOT EXISTS idx_campaigns_segment_active
    ON campaigns(segment, is_active, priority);

CREATE TABLE IF NOT EXISTS campaign_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    segment VARCHAR(80) NOT NULL,
    channel VARCHAR(40) NOT NULL CHECK (channel IN ('email', 'push', 'manual_whatsapp', 'in_app')),
    status VARCHAR(30) NOT NULL CHECK (status IN ('queued', 'sent', 'failed', 'converted')),
    error TEXT,
    rendered_to TEXT,
    subject TEXT,
    body TEXT,
    sent_at TIMESTAMPTZ,
    opened_at TIMESTAMPTZ,
    clicked_at TIMESTAMPTZ,
    converted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_user_created
    ON campaign_deliveries(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_campaign_status
    ON campaign_deliveries(campaign_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_segment_status
    ON campaign_deliveries(segment, status, created_at DESC);

CREATE TABLE IF NOT EXISTS campaign_conversions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id UUID NOT NULL UNIQUE REFERENCES campaign_deliveries(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    conversion_event VARCHAR(80) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_campaign_conversions_campaign_created
    ON campaign_conversions(campaign_id, created_at DESC);

CREATE OR REPLACE FUNCTION growth_engine_record_event(
    p_user_id UUID,
    p_event_name TEXT,
    p_metadata JSONB,
    p_event_at TIMESTAMPTZ
) RETURNS VOID AS $$
DECLARE
    v_stage TEXT := 'signed_up';
    v_is_active BOOLEAN := FALSE;
    v_is_kyc_started BOOLEAN := FALSE;
    v_is_kyc_completed BOOLEAN := FALSE;
    v_is_deposit_started BOOLEAN := FALSE;
    v_is_deposit_completed BOOLEAN := FALSE;
    v_is_miriam_used BOOLEAN := FALSE;
    v_is_allocation_enabled BOOLEAN := FALSE;
BEGIN
    IF p_user_id IS NULL OR p_event_name IS NULL OR p_event_name = '' THEN
        RETURN;
    END IF;

    v_stage := CASE p_event_name
        WHEN 'app_opened' THEN 'opened_app'
        WHEN 'kyc_started' THEN 'kyc_started'
        WHEN 'kyc_completed' THEN 'kyc_completed'
        WHEN 'deposit_completed' THEN 'first_deposit_done'
        WHEN 'inactive_7_days_detected' THEN 'dormant'
        WHEN 'inactive_14_days_detected' THEN 'churned'
        ELSE NULL
    END;

    v_is_active := p_event_name IN (
        'app_opened', 'kyc_started', 'kyc_completed', 'deposit_started',
        'deposit_completed', 'miriam_used', 'allocation_enabled', 'reactivated'
    );
    v_is_kyc_started := p_event_name = 'kyc_started';
    v_is_kyc_completed := p_event_name = 'kyc_completed';
    v_is_deposit_started := p_event_name = 'deposit_started';
    v_is_deposit_completed := p_event_name = 'deposit_completed';
    v_is_miriam_used := p_event_name = 'miriam_used';
    v_is_allocation_enabled := p_event_name = 'allocation_enabled';

    INSERT INTO user_events (user_id, event_name, metadata, created_at)
    VALUES (p_user_id, p_event_name, COALESCE(p_metadata, '{}'::jsonb), COALESCE(p_event_at, NOW()));

    INSERT INTO user_lifecycle (
        user_id, lifecycle_stage, last_active_at, kyc_started_at, kyc_completed_at,
        first_deposit_started_at, first_deposit_at, last_deposit_at, miriam_last_used_at,
        allocation_enabled, created_at, updated_at
    )
    VALUES (
        p_user_id, COALESCE(v_stage, 'signed_up'),
        CASE WHEN v_is_active THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_kyc_started THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_kyc_completed THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_deposit_started THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_deposit_completed THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_deposit_completed THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        CASE WHEN v_is_miriam_used THEN COALESCE(p_event_at, NOW()) ELSE NULL END,
        v_is_allocation_enabled,
        COALESCE(p_event_at, NOW()),
        COALESCE(p_event_at, NOW())
    )
    ON CONFLICT (user_id) DO UPDATE SET
        lifecycle_stage = CASE WHEN v_stage IS NOT NULL THEN v_stage ELSE user_lifecycle.lifecycle_stage END,
        last_active_at = CASE WHEN v_is_active THEN GREATEST(COALESCE(user_lifecycle.last_active_at, COALESCE(p_event_at, NOW())), COALESCE(p_event_at, NOW())) ELSE user_lifecycle.last_active_at END,
        kyc_started_at = CASE WHEN v_is_kyc_started THEN COALESCE(user_lifecycle.kyc_started_at, COALESCE(p_event_at, NOW())) ELSE user_lifecycle.kyc_started_at END,
        kyc_completed_at = CASE WHEN v_is_kyc_completed THEN COALESCE(user_lifecycle.kyc_completed_at, COALESCE(p_event_at, NOW())) ELSE user_lifecycle.kyc_completed_at END,
        first_deposit_started_at = CASE WHEN v_is_deposit_started THEN COALESCE(user_lifecycle.first_deposit_started_at, COALESCE(p_event_at, NOW())) ELSE user_lifecycle.first_deposit_started_at END,
        first_deposit_at = CASE WHEN v_is_deposit_completed THEN COALESCE(user_lifecycle.first_deposit_at, COALESCE(p_event_at, NOW())) ELSE user_lifecycle.first_deposit_at END,
        last_deposit_at = CASE WHEN v_is_deposit_completed THEN GREATEST(COALESCE(user_lifecycle.last_deposit_at, COALESCE(p_event_at, NOW())), COALESCE(p_event_at, NOW())) ELSE user_lifecycle.last_deposit_at END,
        miriam_last_used_at = CASE WHEN v_is_miriam_used THEN GREATEST(COALESCE(user_lifecycle.miriam_last_used_at, COALESCE(p_event_at, NOW())), COALESCE(p_event_at, NOW())) ELSE user_lifecycle.miriam_last_used_at END,
        allocation_enabled = user_lifecycle.allocation_enabled OR v_is_allocation_enabled,
        updated_at = COALESCE(p_event_at, NOW());

    WITH updated AS (
        UPDATE campaign_deliveries cd
        SET status = 'converted', converted_at = COALESCE(p_event_at, NOW())
        FROM campaigns c
        WHERE c.id = cd.campaign_id
          AND cd.user_id = p_user_id
          AND c.conversion_event = p_event_name
          AND cd.converted_at IS NULL
          AND cd.status IN ('queued', 'sent')
        RETURNING cd.id, cd.user_id, cd.campaign_id
    )
    INSERT INTO campaign_conversions (delivery_id, user_id, campaign_id, conversion_event, created_at)
    SELECT id, user_id, campaign_id, p_event_name, COALESCE(p_event_at, NOW())
    FROM updated
    ON CONFLICT (delivery_id) DO NOTHING;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION growth_engine_users_trigger() RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM growth_engine_record_event(NEW.id, 'user_signed_up', jsonb_build_object('source', 'users_insert'), NEW.created_at);
        RETURN NEW;
    END IF;

    IF NEW.last_login_at IS NOT NULL AND (OLD.last_login_at IS NULL OR NEW.last_login_at IS DISTINCT FROM OLD.last_login_at) THEN
        PERFORM growth_engine_record_event(NEW.id, 'app_opened', jsonb_build_object('source', 'users_last_login'), NEW.last_login_at);
    END IF;

    IF NEW.kyc_submitted_at IS NOT NULL AND (OLD.kyc_submitted_at IS NULL OR NEW.kyc_submitted_at IS DISTINCT FROM OLD.kyc_submitted_at) THEN
        PERFORM growth_engine_record_event(NEW.id, 'kyc_started', jsonb_build_object('source', 'users_kyc_submitted'), NEW.kyc_submitted_at);
    ELSIF NEW.kyc_status IN ('processing', 'submitted') AND OLD.kyc_status IS DISTINCT FROM NEW.kyc_status THEN
        PERFORM growth_engine_record_event(NEW.id, 'kyc_started', jsonb_build_object('source', 'users_kyc_status'), COALESCE(NEW.updated_at, NOW()));
    END IF;

    IF NEW.kyc_approved_at IS NOT NULL AND (OLD.kyc_approved_at IS NULL OR NEW.kyc_approved_at IS DISTINCT FROM OLD.kyc_approved_at) THEN
        PERFORM growth_engine_record_event(NEW.id, 'kyc_completed', jsonb_build_object('source', 'users_kyc_approved'), NEW.kyc_approved_at);
    ELSIF NEW.kyc_status IN ('approved', 'active') AND OLD.kyc_status IS DISTINCT FROM NEW.kyc_status THEN
        PERFORM growth_engine_record_event(NEW.id, 'kyc_completed', jsonb_build_object('source', 'users_kyc_status'), COALESCE(NEW.kyc_approved_at, NEW.updated_at, NOW()));
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_growth_engine_users_insert ON users;
CREATE TRIGGER trg_growth_engine_users_insert
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION growth_engine_users_trigger();

DROP TRIGGER IF EXISTS trg_growth_engine_users_update ON users;
CREATE TRIGGER trg_growth_engine_users_update
AFTER UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION growth_engine_users_trigger();

CREATE OR REPLACE FUNCTION growth_engine_deposits_trigger() RETURNS TRIGGER AS $$
DECLARE
    v_event_at TIMESTAMPTZ := COALESCE(NEW.confirmed_at, NEW.created_at, NOW());
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'pending' THEN
            PERFORM growth_engine_record_event(NEW.user_id, 'deposit_started', jsonb_build_object('source', 'deposits_insert', 'deposit_id', NEW.id), NEW.created_at);
        ELSIF NEW.status IN ('confirmed', 'completed', 'broker_funded') THEN
            PERFORM growth_engine_record_event(NEW.user_id, 'deposit_completed', jsonb_build_object('source', 'deposits_insert', 'deposit_id', NEW.id), v_event_at);
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.status IN ('confirmed', 'completed', 'broker_funded') AND OLD.status IS DISTINCT FROM NEW.status THEN
        PERFORM growth_engine_record_event(NEW.user_id, 'deposit_completed', jsonb_build_object('source', 'deposits_status', 'deposit_id', NEW.id), v_event_at);
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_growth_engine_deposits_insert ON deposits;
CREATE TRIGGER trg_growth_engine_deposits_insert
AFTER INSERT ON deposits
FOR EACH ROW EXECUTE FUNCTION growth_engine_deposits_trigger();

DROP TRIGGER IF EXISTS trg_growth_engine_deposits_update ON deposits;
CREATE TRIGGER trg_growth_engine_deposits_update
AFTER UPDATE ON deposits
FOR EACH ROW EXECUTE FUNCTION growth_engine_deposits_trigger();

INSERT INTO notification_templates (template_key, channel, subject, body, cta) VALUES
    (
        'founder_signup_recovery_email',
        'email',
        'Thanks for signing up for Rail Money',
        'Hey {{name}},

I''m Tobi, founder of Rail Money. Thank you for signing up.

I built Rail because I''ve seen how easy it is for money to come in and disappear without a real system behind it. Most people don''t need another finance app full of charts. They need something that quietly helps them make progress.

That''s what Rail is built for.

Rail helps you automatically set aside money for your future while keeping the rest available to spend normally. Miriam, our financial intelligence agent, is there to help you understand and manage your money without overthinking everything.

I''d love for you to try the app and send me honest feedback.

Thanks again for being early.

Tobi
Founder
Rail Money',
        'Finish setup'
    ),
    (
        'kyc_abandoned_push',
        'push',
        'Your Rail setup is almost ready',
        'Miriam is waiting on one setup step so Rail can start organizing your money.',
        'Finish setup'
    ),
    (
        'kyc_trust_email',
        'email',
        'You''re almost done with Rail setup',
        'Hey {{name}},

Rail needs this setup step to keep your account secure and ready for money movement. You''re almost done.

Finish setup and Miriam can start helping you organize your money.',
        'Finish setup'
    ),
    (
        'first_deposit_test_email',
        'email',
        'Try Rail with a small amount first',
        'Hey {{name}},

Try Rail with a small amount first. Start with NGN 1,000 or $1 and see how Miriam organizes your money for 7 days.',
        'Start the 7-Day Rail Test'
    ),
    (
        'miriam_reactivation_push',
        'push',
        'Miriam has your weekly money check-in ready',
        'See what changed and what to do next.',
        'Open Miriam'
    ),
    (
        'product_update_winback_email',
        'email',
        'Rail has changed',
        'Hey {{name}},

Rail has changed. Miriam is now the center of the app - your financial agent for organizing money and building progress automatically.',
        'See what changed'
    ),
    (
        'manual_whatsapp_signup_recovery',
        'manual_whatsapp',
        'Manual WhatsApp signup recovery',
        'Hey {{name}}, your Rail setup is almost ready. Finish setup so Miriam can start organizing your money.',
        'Finish setup'
    )
ON CONFLICT (template_key) DO UPDATE SET
    subject = EXCLUDED.subject,
    body = EXCLUDED.body,
    cta = EXCLUDED.cta,
    updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, from_email, from_name, reply_to, priority)
SELECT 'Founder signup recovery', 'signup_no_kyc', 'email', template_key, subject, body, cta, 14, 'kyc_started', 'tobilobaomotade@userail.money', 'Tobi from Rail Money', 'tobilobaomotade@userail.money', 10
FROM notification_templates WHERE template_key = 'founder_signup_recovery_email'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT 'KYC abandoned push', 'kyc_abandoned', 'push', template_key, subject, body, cta, 2, 'kyc_completed', 10
FROM notification_templates WHERE template_key = 'kyc_abandoned_push'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT 'KYC trust email', 'kyc_abandoned', 'email', template_key, subject, body, cta, 7, 'kyc_completed', 20
FROM notification_templates WHERE template_key = 'kyc_trust_email'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT '7-Day Rail Test', 'kyc_no_deposit', 'email', template_key, subject, body, cta, 7, 'deposit_completed', 10
FROM notification_templates WHERE template_key = 'first_deposit_test_email'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT 'Miriam weekly check-in', 'miriam_user_inactive', 'push', template_key, subject, body, cta, 7, 'miriam_used', 10
FROM notification_templates WHERE template_key = 'miriam_reactivation_push'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT 'Rail changed win-back', 'fully_churned', 'email', template_key, subject, body, cta, 14, 'app_opened', 10
FROM notification_templates WHERE template_key = 'product_update_winback_email'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

INSERT INTO campaigns (name, segment, channel, template_key, subject, body, cta, cooldown_days, conversion_event, priority)
SELECT 'Manual WhatsApp signup recovery', 'signup_no_kyc', 'manual_whatsapp', template_key, subject, body, cta, 3, 'kyc_started', 30
FROM notification_templates WHERE template_key = 'manual_whatsapp_signup_recovery'
ON CONFLICT (segment, channel, template_key) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, cta = EXCLUDED.cta, is_active = TRUE, updated_at = NOW();

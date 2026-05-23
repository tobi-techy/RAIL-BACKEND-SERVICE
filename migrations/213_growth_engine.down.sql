DROP TRIGGER IF EXISTS trg_growth_engine_deposits_update ON deposits;
DROP TRIGGER IF EXISTS trg_growth_engine_deposits_insert ON deposits;
DROP TRIGGER IF EXISTS trg_growth_engine_users_update ON users;
DROP TRIGGER IF EXISTS trg_growth_engine_users_insert ON users;
DROP FUNCTION IF EXISTS growth_engine_deposits_trigger();
DROP FUNCTION IF EXISTS growth_engine_users_trigger();
DROP FUNCTION IF EXISTS growth_engine_record_event(UUID, TEXT, JSONB, TIMESTAMPTZ);

DROP TABLE IF EXISTS campaign_conversions;
DROP TABLE IF EXISTS campaign_deliveries;
DROP TABLE IF EXISTS campaigns;
DROP TABLE IF EXISTS notification_templates;
DROP TABLE IF EXISTS growth_segments;
DROP TABLE IF EXISTS user_lifecycle;
DROP TABLE IF EXISTS user_events;

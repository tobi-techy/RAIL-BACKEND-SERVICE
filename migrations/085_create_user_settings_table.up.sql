-- Migration: Create User Settings Table
-- Purpose: Store user-specific settings like nickname and currency locale

CREATE TABLE IF NOT EXISTS user_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    nickname VARCHAR(50),
    currency_locale VARCHAR(10) NOT NULL DEFAULT 'en-US',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_settings_user_id ON user_settings(user_id);

CREATE TRIGGER update_user_settings_updated_at
    BEFORE UPDATE ON user_settings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

COMMENT ON TABLE user_settings IS 'User-specific settings for display preferences';
COMMENT ON COLUMN user_settings.nickname IS 'Display name for multi-account support';
COMMENT ON COLUMN user_settings.currency_locale IS 'Locale for currency formatting (e.g., en-US, de-DE)';

-- Drop triggers
DROP TRIGGER IF EXISTS update_user_rate_limits_updated_at ON user_rate_limits;
DROP TRIGGER IF EXISTS update_api_keys_updated_at ON api_keys;
DROP TRIGGER IF EXISTS update_user_2fa_updated_at ON user_2fa;

-- Drop indexes
DROP INDEX IF EXISTS idx_user_rate_limits_user_endpoint;
DROP INDEX IF EXISTS idx_user_rate_limits_window;
DROP INDEX IF EXISTS idx_api_keys_hash;
DROP INDEX IF EXISTS idx_api_keys_user_id;
DROP INDEX IF EXISTS idx_api_keys_active;
DROP INDEX IF EXISTS idx_user_2fa_user_id;
DROP INDEX IF EXISTS idx_sessions_device_fingerprint;

-- Remove session columns
ALTER TABLE sessions DROP COLUMN IF EXISTS device_fingerprint;
ALTER TABLE sessions DROP COLUMN IF EXISTS location;
ALTER TABLE sessions DROP COLUMN IF EXISTS concurrent_limit;

-- Drop tables
DROP TABLE IF EXISTS user_2fa;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS user_rate_limits;
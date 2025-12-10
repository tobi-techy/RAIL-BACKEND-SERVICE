-- Remove security-related columns from users table
ALTER TABLE users DROP COLUMN IF EXISTS security_level;
ALTER TABLE users DROP COLUMN IF EXISTS require_ip_whitelist;
ALTER TABLE users DROP COLUMN IF EXISTS require_device_verification;
ALTER TABLE users DROP COLUMN IF EXISTS last_password_change;

-- Drop tables
DROP TABLE IF EXISTS password_history;
DROP TABLE IF EXISTS security_events;
DROP TABLE IF EXISTS withdrawal_confirmations;
DROP TABLE IF EXISTS ip_whitelist;
DROP TABLE IF EXISTS known_devices;

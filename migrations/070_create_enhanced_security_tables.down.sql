-- Drop function
DROP FUNCTION IF EXISTS cleanup_old_login_attempts();

-- Drop tables
DROP TABLE IF EXISTS login_attempts;
DROP TABLE IF EXISTS secret_rotations;
DROP TABLE IF EXISTS refresh_token_rotations;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_rate_limits;

-- Remove columns from users table
ALTER TABLE users DROP COLUMN IF EXISTS password_bcrypt_cost;
ALTER TABLE users DROP COLUMN IF EXISTS password_expires_at;
ALTER TABLE users DROP COLUMN IF EXISTS require_password_change;

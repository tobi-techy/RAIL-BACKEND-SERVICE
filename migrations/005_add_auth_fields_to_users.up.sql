-- Add authentication-related fields to users table

-- Add password hash field for local authentication
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash VARCHAR(255);

-- Add user role field
ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'user' CHECK (
    role IN ('user', 'admin', 'super_admin')
);

-- Add active status field  
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_active BOOLEAN DEFAULT TRUE;

-- Add last login timestamp
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMP;

-- Add KYC provider reference for tracking
ALTER TABLE users ADD COLUMN IF NOT EXISTS kyc_provider_ref VARCHAR(255);

-- Add KYC submission timestamp
ALTER TABLE users ADD COLUMN IF NOT EXISTS kyc_submitted_at TIMESTAMP;

-- Create additional indexes for new fields
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_is_active ON users(is_active);
CREATE INDEX IF NOT EXISTS idx_users_kyc_provider_ref ON users(kyc_provider_ref);

-- Update onboarding status check constraint to match our enum
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check;
ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check CHECK (
    onboarding_status IN ('started', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed')
);

-- Update KYC status check constraint to match our enum
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired')
);

-- Update comments
COMMENT ON COLUMN users.password_hash IS 'Bcrypt hashed password for local authentication';
COMMENT ON COLUMN users.role IS 'User role for authorization (user, admin, super_admin)';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active';
COMMENT ON COLUMN users.last_login_at IS 'Timestamp of last successful login';
COMMENT ON COLUMN users.kyc_provider_ref IS 'Reference ID from KYC provider for tracking submissions';
COMMENT ON COLUMN users.kyc_submitted_at IS 'Timestamp when KYC documents were first submitted';
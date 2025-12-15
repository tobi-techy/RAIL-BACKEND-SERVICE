-- Remove authentication-related fields from users table

-- Drop the additional indexes
DROP INDEX IF EXISTS idx_users_role;
DROP INDEX IF EXISTS idx_users_is_active;
DROP INDEX IF EXISTS idx_users_kyc_provider_ref;

-- Remove the added columns
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
ALTER TABLE users DROP COLUMN IF EXISTS role;
ALTER TABLE users DROP COLUMN IF EXISTS is_active;
ALTER TABLE users DROP COLUMN IF EXISTS last_login_at;
ALTER TABLE users DROP COLUMN IF EXISTS kyc_provider_ref;
ALTER TABLE users DROP COLUMN IF EXISTS kyc_submitted_at;

-- Restore original onboarding status constraint
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check;
ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check CHECK (
    onboarding_status IN ('started', 'email_verified', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed')
);

-- Restore original KYC status constraint
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected')
);
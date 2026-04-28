-- Add basic_complete to onboarding_status check constraint
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_onboarding_status;
ALTER TABLE users ADD CONSTRAINT chk_onboarding_status
    CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check;
ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check
    CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

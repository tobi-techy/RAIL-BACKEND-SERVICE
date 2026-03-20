ALTER TABLE onboarding_flows DROP CONSTRAINT IF EXISTS chk_step;
ALTER TABLE onboarding_flows ADD CONSTRAINT chk_step
    CHECK (step IN ('registration', 'email_verification', 'phone_verification', 'kyc_submission', 'kyc_review', 'wallet_creation', 'completed'));

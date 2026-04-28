-- Ensure non_kyc is allowed in ALL kyc_status constraints before backfilling.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired', 'non_kyc')
);
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_kyc_status;
ALTER TABLE users ADD CONSTRAINT chk_kyc_status CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired', 'non_kyc')
);

-- Update existing users who completed basic onboarding but have pending KYC status.
UPDATE users
SET kyc_status = 'non_kyc', updated_at = NOW()
WHERE kyc_status = 'pending'
  AND onboarding_status IN ('basic_complete', 'wallets_pending', 'completed')
  AND email_verified = true;

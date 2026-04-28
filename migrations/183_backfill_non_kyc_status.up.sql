-- Update existing users who completed basic onboarding but have pending KYC status.
-- These users should be able to use limited crypto features without full KYC.
UPDATE users
SET kyc_status = 'non_kyc', updated_at = NOW()
WHERE kyc_status = 'pending'
  AND onboarding_status IN ('basic_complete', 'wallets_pending', 'completed')
  AND email_verified = true;

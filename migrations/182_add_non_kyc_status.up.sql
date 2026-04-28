-- Add 'non_kyc' to kyc_status CHECK constraint for Circle wallet users without KYC.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired', 'non_kyc')
);

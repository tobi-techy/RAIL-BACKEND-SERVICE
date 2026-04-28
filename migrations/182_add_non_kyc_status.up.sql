-- Add 'non_kyc' to ALL kyc_status CHECK constraints.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired', 'non_kyc')
);

ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_kyc_status;
ALTER TABLE users ADD CONSTRAINT chk_kyc_status CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired', 'non_kyc')
);

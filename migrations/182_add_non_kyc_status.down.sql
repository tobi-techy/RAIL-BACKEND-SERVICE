-- Revert: remove 'non_kyc' from kyc_status CHECK constraints.
-- WARNING: will fail if any rows have kyc_status = 'non_kyc'.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired')
);

ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_kyc_status;
ALTER TABLE users ADD CONSTRAINT chk_kyc_status CHECK (
    kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired')
);

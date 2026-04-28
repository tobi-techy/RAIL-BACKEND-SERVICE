-- Revert non_kyc users back to pending.
UPDATE users
SET kyc_status = 'pending', updated_at = NOW()
WHERE kyc_status = 'non_kyc';

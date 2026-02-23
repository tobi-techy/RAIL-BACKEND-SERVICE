ALTER TABLE users
DROP COLUMN IF EXISTS bridge_kyc_status,
DROP COLUMN IF EXISTS bridge_kyc_link;

DROP INDEX IF EXISTS idx_users_graph_person_id;

ALTER TABLE users DROP COLUMN IF EXISTS bvn_last4;
ALTER TABLE users DROP COLUMN IF EXISTS bvn_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS kyc_tier;
ALTER TABLE users DROP COLUMN IF EXISTS graph_person_id;

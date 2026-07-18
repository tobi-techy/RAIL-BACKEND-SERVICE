-- Tiered KYC + Graph identity linkage on users.
-- kyc_tier: 1 = non_kyc (crypto only), 2 = basic (BVN + local ID, NGN account),
--           3 = advanced (passport + proof of address, USD VA/cards/investing).
-- BVN itself is never stored (transient PII); only a verification flag + last-4.
ALTER TABLE users ADD COLUMN IF NOT EXISTS graph_person_id VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS kyc_tier SMALLINT NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN IF NOT EXISTS bvn_verified_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS bvn_last4 VARCHAR(4);

CREATE INDEX IF NOT EXISTS idx_users_graph_person_id ON users(graph_person_id) WHERE graph_person_id IS NOT NULL;

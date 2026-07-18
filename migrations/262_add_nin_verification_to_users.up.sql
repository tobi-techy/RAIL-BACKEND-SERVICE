-- NIN (National Identification Number) verification linkage on users.
-- Tier 2 unlock is BVN + NIN (NIN preferred; voter's card / DL / passport allowed).
-- The NIN itself is never stored (transient PII); only a verification flag + last-4.
ALTER TABLE users ADD COLUMN IF NOT EXISTS nin_verified_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS nin_last4 VARCHAR(4);

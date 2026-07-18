-- Backfill persisted kyc_tier so the tier ladder is authoritative after
-- EffectiveKYCTier stops inferring Tier 3 from the legacy status string.
--   Tier 3 (advanced): Bridge KYC active, or a legacy advanced_* status.
--   Tier 2 (basic):    BVN + NIN both verified via Graph.
-- Users left at their existing kyc_tier otherwise. Only ever raises the floor.

-- Tier 3: Bridge active or legacy advanced status.
UPDATE users
SET kyc_tier = 3
WHERE COALESCE(kyc_tier, 1) < 3
  AND (
        LOWER(COALESCE(bridge_kyc_status, '')) = 'active'
     OR LOWER(COALESCE(kyc_status, '')) IN ('advanced_approved', 'advanced_verified')
  );

-- Tier 2: BVN + NIN verified (Graph NGN eligible) but not already advanced.
UPDATE users
SET kyc_tier = 2
WHERE COALESCE(kyc_tier, 1) < 2
  AND bvn_verified_at IS NOT NULL
  AND nin_verified_at IS NOT NULL;

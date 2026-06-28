-- Refuse to drop the column while any non-terminal redemption still depends on it:
-- finalizeRedemption reads pre_redeem_eoa_balance to verify funds arrived, so dropping it
-- mid-flight would break finalization and lose the snapshot. Complete or fail those first.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM blend_yield_redemptions
        WHERE status IN ('pending', 'quoted', 'executing', 'submitted')
    ) THEN
        RAISE EXCEPTION 'Cannot drop pre_redeem_eoa_balance: in-flight redemptions still depend on it. Complete or fail all non-terminal redemptions (pending/quoted/executing/submitted) before rolling back.';
    END IF;
END $$;

ALTER TABLE blend_yield_redemptions
    DROP COLUMN IF EXISTS pre_redeem_eoa_balance;

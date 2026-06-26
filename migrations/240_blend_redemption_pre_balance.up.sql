-- Snapshot of the user's EOA on-chain USDC balance captured BEFORE a redemption's
-- withdraw executes. finalizeRedemption uses it to assert the balance increased by the
-- redeemed amount (have >= pre_redeem_eoa_balance + amount) rather than merely being
-- >= amount, which prevents concurrent redemptions from passing on the same balance.
ALTER TABLE blend_yield_redemptions
    ADD COLUMN IF NOT EXISTS pre_redeem_eoa_balance NUMERIC(36,18);

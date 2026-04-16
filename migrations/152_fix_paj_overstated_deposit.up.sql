-- Fix overstated PAJ deposit for user a28c1e1a-3e6d-4a3d-9fec-8186396cc478
-- PAJ reported 1.06 USDC but only 0.893659 USDC arrived on-chain (verified via Solana tx)
-- Adjustment: -0.166341 USDC total (split 70/30)

DO $$
DECLARE
  v_user_id UUID := 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478';
  v_overstated NUMERIC := 0.166341;
  v_spend_adj NUMERIC := ROUND(v_overstated * 0.70, 6);
  v_stash_adj NUMERIC := v_overstated - v_spend_adj;
BEGIN
  -- Adjust USDC balance
  UPDATE ledger_accounts SET balance = balance - v_overstated
  WHERE user_id = v_user_id AND account_type = 'usdc_balance' AND balance >= v_overstated;

  -- Adjust spending balance
  UPDATE ledger_accounts SET balance = balance - v_spend_adj
  WHERE user_id = v_user_id AND account_type = 'spending_balance' AND balance >= v_spend_adj;

  -- Adjust stash balance
  UPDATE ledger_accounts SET balance = balance - v_stash_adj
  WHERE user_id = v_user_id AND account_type = 'stash_balance' AND balance >= v_stash_adj;

  RAISE NOTICE 'Adjusted: USDC -%, Spend -%, Stash -%', v_overstated, v_spend_adj, v_stash_adj;
END $$;

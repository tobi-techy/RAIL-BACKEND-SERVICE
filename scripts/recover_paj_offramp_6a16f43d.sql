-- Recovery script for stuck Paj offramp order 6a16f43dea3e24e2789e1c4c
--
-- What happened:
--   1. Circle transfer adapter was not configured → reverseHold was called.
--   2. reverseHold passed rail_fee=50 (NGN) instead of the USDC-denominated fee
--      into the ledger metadata. withdrawalPlatformFeeFromMetadata rejected it
--      because 50 > 0.7766 USDC → ledger reversal failed.
--   3. The order was marked failed + deposit_id set, but the ledger hold was
--      never reversed. User's 0.7766 USDC is stuck.
--
-- Fix: reset the order to pending with deposit_id=NULL so the paj_offramp_recovery
-- worker picks it up and reverses the hold correctly (the code bug is now fixed).
--
-- Safety check: only resets if no matching reversal ledger transaction exists.

BEGIN;

-- Verify the order state before touching it.
SELECT
    paj_order_id,
    user_id,
    status,
    hold_amount,
    deposit_id,
    created_at
FROM paj_orders
WHERE paj_order_id = '6a16f43dea3e24e2789e1c4c';

-- Confirm no reversal ledger tx exists for this order.
SELECT idempotency_key, amount, created_at
FROM ledger_transactions
WHERE idempotency_key LIKE '%6a16f43dea3e24e2789e1c4c%';

-- Reset to pending so the recovery worker can reverse the hold.
-- Only runs if status=failed AND no reversal tx exists.
UPDATE paj_orders
SET
    status     = 'pending',
    deposit_id = NULL,
    updated_at = NOW()
WHERE paj_order_id = '6a16f43dea3e24e2789e1c4c'
  AND status = 'failed'
  AND NOT EXISTS (
      SELECT 1 FROM ledger_transactions
      WHERE idempotency_key LIKE '%6a16f43dea3e24e2789e1c4c%'
        AND transaction_type = 'reversal'
  );

-- Confirm the update.
SELECT paj_order_id, status, deposit_id, updated_at
FROM paj_orders
WHERE paj_order_id = '6a16f43dea3e24e2789e1c4c';

COMMIT;

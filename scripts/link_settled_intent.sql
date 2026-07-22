-- Link the SETTLED Blend intent to the redemption.
-- After running, the worker's awaitWithdrawSettlement will see SETTLED and finalize.

BEGIN;

-- Update the redemption with the settled intent (skips already-complete/failed rows)
UPDATE blend_yield_redemptions
SET intent_id = '7240b8cc-40a9-443c-86e9-2c9fa319fad4',
    intent_status = 'SETTLED',
    status = 'submitted',
    tx_hash = '0x828666ec59d4617765155aabc1526e52ae4fd4316780adb5585a256198a5ab26',
    submitted_at = NOW(),
    next_retry_at = NOW(),
    updated_at = NOW()
WHERE id = '718d22db-0000-0000-0000-000000000000'
  AND status NOT IN ('complete', 'failed');

-- Verify exactly one row was updated
DO $$
BEGIN
  IF (SELECT count(*) FROM blend_yield_redemptions
      WHERE id = '718d22db-0000-0000-0000-000000000000') = 0 THEN
    RAISE EXCEPTION 'redemption 718d22db not found';
  END IF;
  IF (SELECT status FROM blend_yield_redemptions
      WHERE id = '718d22db-0000-0000-0000-000000000000') NOT IN ('submitted') THEN
    RAISE EXCEPTION 'redemption 718d22db was not updated (status unchanged — may already be complete/failed)';
  END IF;
END $$;

-- Show final state
SELECT id, status, intent_id, intent_status, tx_hash, attempts, updated_at
FROM blend_yield_redemptions
WHERE id = '718d22db-0000-0000-0000-000000000000';

COMMIT;

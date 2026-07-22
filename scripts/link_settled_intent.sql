-- Link the SETTLED Blend intent to the redemption.
-- After running, the worker's awaitWithdrawSettlement will see SETTLED and finalize.

BEGIN;

-- Update the redemption with the settled intent
UPDATE blend_yield_redemptions
SET intent_id = '7240b8cc-40a9-443c-86e9-2c9fa319fad4',
    intent_status = 'SETTLED',
    status = 'submitted',
    tx_hash = '0x828666ec59d4617765155aabc1526e52ae4fd4316780adb5585a256198a5ab26',
    submitted_at = NOW(),
    next_retry_at = NOW(),
    updated_at = NOW()
WHERE id = '718d22db-0000-0000-0000-000000000000'
  AND status != 'completed'
RETURNING id, status, intent_id, intent_status, tx_hash, attempts, updated_at;

COMMIT;

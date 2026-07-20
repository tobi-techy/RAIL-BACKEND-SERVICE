-- Link the LOCKED Blend intent to the redemption and set status so awaitWithdrawSettlement picks it up.
-- Run this AFTER submitting the tx hash via submit_locked_tx_hash.sh

BEGIN;

-- Find the redemption and set its intent_id to the LOCKED one
UPDATE blend_yield_redemptions
SET intent_id = '7240b8cc-40a9-443c-86e9-2c9fa319fad4',
    intent_status = 'LOCKED',
    status = 'submitted',
    tx_hash = '0x828666ec59d4617765155aabc1526e52ae4fd4316780adb5585a256198a5ab26',
    submitted_at = NOW(),
    next_retry_at = NOW() + INTERVAL '5 minutes',
    updated_at = NOW()
WHERE id::text LIKE '718d22db%'
  AND status != 'completed';

-- Verify
SELECT id, status, intent_id, intent_status, tx_hash, attempts, updated_at
FROM blend_yield_redemptions
WHERE id::text LIKE '718d22db%';

COMMIT;

-- Cancel the OPEN intent and stop the redemption from retrying.
-- The $3.11 is settled on Blend (intent 7240b8cc) but vault flow plans keep failing.
BEGIN;

-- Cancel via the Blend API first:
-- curl -X POST "https://api.portal.blend.money/extern/svr/YOUR_ACCOUNT_TYPE_ID/account/17ee28ec-51e8-41fa-82b7-7005642975c7/intent/9ef367a9-dfcc-4ade-ba3d-b4c1dd1db077/cancel" \
--   -H "X-API-Key: YOUR_API_KEY" -H "Content-Type: application/json" -d '{}'

-- Mark redemption as needing manual resolution
UPDATE blend_yield_redemptions
SET status = 'failed',
    intent_status = 'SETTLED',
    last_error = 'manual: vault flow plans failing, $3.11 settled on Blend but not bridged to EOA',
    updated_at = NOW()
WHERE id::text LIKE '718d22db%'
  AND status NOT IN ('completed', 'failed');

SELECT id, status, intent_id, intent_status, tx_hash, attempts, last_error, updated_at
FROM blend_yield_redemptions
WHERE id::text LIKE '718d22db%';

COMMIT;

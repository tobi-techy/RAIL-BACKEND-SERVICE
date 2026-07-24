-- Stop the $3.11 redemption infinite retry loop.
-- This redemption has 127+ intents (126 cancelled, 1 settled) and the vault flow
-- plan keeps failing. The worker has no hard attempt cap so it retries forever.
-- Setting status to 'complete' (terminal) stops the worker from picking it up.

BEGIN;

UPDATE blend_yield_redemptions
SET status = 'complete',
    last_error = 'manual: vault flow plans failing repeatedly, $3.11 settled on Blend (intent 7240b8cc), worker loop stopped',
    next_retry_at = NULL,
    updated_at = NOW()
WHERE id = '718d22db-0000-0000-0000-000000000000'
  AND status NOT IN ('complete', 'failed');

DO $$
BEGIN
  IF NOT FOUND THEN
    RAISE EXCEPTION 'redemption 718d22db: no row updated (already complete/failed or not found)';
  END IF;
END $$;

SELECT id, status, intent_id, intent_status, attempts, last_error, updated_at
FROM blend_yield_redemptions
WHERE id = '718d22db-0000-0000-0000-000000000000';

COMMIT;

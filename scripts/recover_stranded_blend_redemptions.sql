-- Recover stranded Blend redemption for user a28c1e1a-3e6d-4a3d-9fec-8186396cc478
-- Root cause: Circle async tx returned empty txHash, executor didn't poll for it,
-- Blend session expired with "signing inactivity". Funds are still in Blend Safe.
--
-- STEP 1: Inspect current state (run first, review before proceeding)
-- STEP 2: Reset the $3.108 redemption to pending (worker will retry with fixed executor)
-- STEP 3: Verify cleanup

-- ============================================================
-- STEP 1: Inspect current state
-- ============================================================

-- 1a. Check the target redemption ($3.108)
SELECT id, user_id, amount::text, status, attempts, last_error,
       intent_id, tx_hash, idempotency_key, created_at, updated_at
FROM blend_yield_redemptions
WHERE id = '718d22db-fd31-44be-a321-204fbcef06a4';

-- 1b. Check the linked withdrawal
SELECT w.id, w.user_id, w.amount::text, w.fee_amount::text, w.status,
       w.source_account, w.idempotency_key, w.provider_transfer_id,
       w.created_at, w.updated_at
FROM withdrawals w
WHERE w.user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND w.idempotency_key = 'withdrawal-718d22db-fd31-44be-a321-204fbcef06a4';

-- 1c. Check ledger entry for this withdrawal
SELECT idempotency_key, status, amount::text, created_at, updated_at
FROM ledger_transactions
WHERE idempotency_key = 'withdrawal-ledger-718d22db-fd31-44be-a321-204fbcef06a4';

-- ============================================================
-- STEP 2: Reset the $3.108 redemption to pending (review STEP 1 first!)
-- ============================================================
-- The worker runs every 30s and will pick this up automatically.
-- The fixed executor will poll Circle for the real txHash before submitting.

UPDATE blend_yield_redemptions
SET status = 'pending',
    intent_id = NULL,
    intent_status = NULL,
    quote_payload = NULL,
    tx_hash = NULL,
    submitted_at = NULL,
    settled_at = NULL,
    last_error = NULL,
    next_retry_at = NOW(),
    updated_at = NOW()
WHERE id = '718d22db-fd31-44be-a321-204fbcef06a4'
  AND status = 'failed';

-- ============================================================
-- STEP 3: Verify the reset worked
-- ============================================================

SELECT id, amount::text, status, attempts, last_error, next_retry_at, updated_at
FROM blend_yield_redemptions
WHERE id = '718d22db-fd31-44be-a321-204fbcef06a4';

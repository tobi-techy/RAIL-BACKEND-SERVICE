-- Recover stuck Blend redemption targeting Ethereum (chain 1)
-- User: a28c1e1a-3e6d-4a3d-9fec-8186396cc478
-- Wallet: 0x28f0dE1b0e96abcfAFF30c4bA48E6d48F50b0Ead (Circle EOA)
-- User's ETH address: 0xd73ab8e5fa23fe1a2242b7b80f1f6a0115a6d43f
--
-- ROOT CAUSE: Withdrawal payload's chainId was set to Ethereum mainnet (chain 1)
-- but the Circle EOA wallet hasn't been deployed on Ethereum — only on Base.
--
-- FIX:
--   STEP 1 — Inspect current state (run this first)
--   STEP 2 — Deploy Safe on Ethereum via Blend API (API call, not SQL)
--   STEP 3 — Reset redemption so worker retries (run after STEP 2 confirms success)
--   STEP 4 — Verify

-- ============================================================
-- STEP 1: Inspect current state
-- ============================================================

-- 1a. Find the user's Blend accounts
SELECT id, user_id, eoa_address, blend_account_id, safe_address, chain_id, safe_status, safe_requested_at, safe_deployed_at, circle_wallet_id
FROM blend_user_accounts
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478';

-- 1b. Check redemptions (latest first)
SELECT id, amount::text, destination_chain_id, status, attempts, last_error, intent_id, tx_hash, created_at, updated_at
FROM blend_yield_redemptions
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
ORDER BY created_at DESC;

-- 1c. Check if there's already a blend account for chain 1 (Ethereum)
-- If not, STEP 2 will create it via the API.
SELECT *
FROM blend_user_accounts
WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478'
  AND chain_id = 1;

-- ============================================================
-- STEP 2: Deploy Safe on Ethereum (API call — NOT SQL)
-- ============================================================
-- From STEP 1a, note the blend_account_id (e.g. 'acct_xxx').
-- Then call (via curl, Postman, or the app's CLI):
--
--   curl -X POST "https://api.blend.money/extern/svr/{accountTypeId}/account/{blendAccountId}/safe/request" \
--     -H "x-api-key: $BLEND_API_KEY" \
--     -H "Content-Type: application/json" \
--     -d '{"chainId": 1}'
--
-- The blend_account_id is the value from STEP 1a.
-- Alternatively, if the app is running locally with the right DB, you can
-- deploy a route with chain_id=1 and the existing handleNewDeposit flow will
-- call RequestSafe automatically. The simplest way is a direct curl.
--
-- After calling, wait ~30s for the Safe to deploy, then check:
--   SELECT * FROM blend_user_accounts
--   WHERE user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478' AND chain_id = 1;
-- (safe_status should be 'validated' and safe_address should be set)

-- ============================================================
-- STEP 3: Update redemption to target Ethereum and reset to pending
-- ============================================================
-- Run THIS AFTER STEP 2 confirms the Safe is deployed on Ethereum.
-- Replace '<REDEMPTION_ID>' with the actual ID from STEP 1b.
-- If destination_chain_id is already 1, we just reset the status.

-- 3a. If the redemption has destination_chain_id = 8453 (Base) but should target Ethereum:
-- UPDATE blend_yield_redemptions
-- SET destination_chain_id = 1,
--     status = 'pending',
--     intent_id = NULL,
--     intent_status = NULL,
--     quote_payload = NULL,
--     tx_hash = NULL,
--     submitted_at = NULL,
--     settled_at = NULL,
--     last_error = NULL,
--     attempts = 0,
--     next_retry_at = NOW(),
--     updated_at = NOW()
-- WHERE id = '<REDEMPTION_ID>'
--   AND user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478';

-- 3b. If destination_chain_id is already 1 (Ethereum), just reset:
-- UPDATE blend_yield_redemptions
-- SET status = 'pending',
--     intent_id = NULL,
--     intent_status = NULL,
--     quote_payload = NULL,
--     tx_hash = NULL,
--     submitted_at = NULL,
--     settled_at = NULL,
--     last_error = NULL,
--     attempts = 0,
--     next_retry_at = NOW(),
--     updated_at = NOW()
-- WHERE id = '<REDEMPTION_ID>'
--   AND status IN ('failed', 'pending')
--   AND user_id = 'a28c1e1a-3e6d-4a3d-9fec-8186396cc478';

-- ============================================================
-- STEP 4: Verify
-- ============================================================
-- SELECT id, amount::text, destination_chain_id, status, attempts, last_error, next_retry_at, updated_at
-- FROM blend_yield_redemptions
-- WHERE id = '<REDEMPTION_ID>';

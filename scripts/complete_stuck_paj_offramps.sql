-- Complete stuck Paj offramp orders where:
--   1. Circle USDC transfer was initiated (bridge_transfer_id IS NOT NULL)
--   2. Status is still 'pending' (webhook was never properly processed)
--   3. Order is old enough that Paj has had time to process (> 2 hours)
--
-- Safety: only updates orders that have not been claimed (deposit_id IS NULL)
-- and have no existing reversal ledger entry. Does NOT affect orders where
-- the Circle transfer never started (those are handled by the offramp recovery
-- worker which reverses the hold).

BEGIN;

-- Preview the orders that will be affected.
SELECT
    paj_order_id,
    user_id,
    order_type,
    status,
    fiat_amount,
    token_amount,
    currency,
    bridge_transfer_id,
    hold_amount,
    created_at,
    CASE
        WHEN created_at < NOW() - interval '24 hours' THEN '>24h'
        WHEN created_at < NOW() - interval '2 hours' THEN '>2h'
        ELSE '<2h'
    END as age_bucket
FROM paj_orders
WHERE order_type = 'offramp'
  AND status = 'pending'
  AND bridge_transfer_id IS NOT NULL
  AND deposit_id IS NULL
  AND created_at < NOW() - interval '2 hours'
ORDER BY created_at DESC;

-- Complete the orders.
UPDATE paj_orders
SET
    status     = 'completed',
    updated_at = NOW()
WHERE order_type = 'offramp'
  AND status = 'pending'
  AND bridge_transfer_id IS NOT NULL
  AND deposit_id IS NULL
  AND created_at < NOW() - interval '2 hours'
  AND NOT EXISTS (
      SELECT 1 FROM ledger_transactions
      WHERE idempotency_key LIKE '%paj-offramp-%' || paj_orders.paj_order_id || '%'
        AND transaction_type = 'reversal'
  );

-- Confirm the update.
SELECT
    paj_order_id,
    user_id,
    status,
    updated_at
FROM paj_orders
WHERE order_type = 'offramp'
  AND status = 'completed'
  AND updated_at > NOW() - interval '1 minute'
ORDER BY updated_at DESC;

COMMIT;

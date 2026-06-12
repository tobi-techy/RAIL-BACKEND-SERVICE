-- Complete Paj offramp orders stuck in 'pending' where the Circle USDC transfer
-- was initiated (bridge_transfer_id IS NOT NULL) but the webhook was never properly
-- processed (typically due to expired Paj session in HandleWebhook).
--
-- Safety: only updates orders that have not been claimed by the recovery worker
-- (deposit_id IS NULL) and are old enough that Paj has had time to deliver NGN
-- (> 2 hours). Skips orders with any existing reversal ledger entry.

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

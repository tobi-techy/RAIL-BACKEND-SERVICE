-- Fix stuck PAJ onramp deposits where the old code claimed the order
-- (set deposit_id) but returned early without crediting because
-- used_user_wallet=true and Bridge webhook hadn't fired yet.
--
-- This resets deposit_id to NULL so the next poll triggers the new
-- fallback credit path.
--
-- Safety: only resets orders that have deposit_id set but NO matching
-- ledger transaction (i.e. never actually credited).

UPDATE paj_orders po
SET deposit_id = NULL
WHERE po.order_type = 'onramp'
  AND po.status = 'completed'
  AND po.used_user_wallet = true
  AND po.deposit_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM ledger_transactions lt
    WHERE lt.idempotency_key = 'paj-onramp-' || po.paj_order_id
  )
  AND NOT EXISTS (
    SELECT 1 FROM deposits d
    WHERE d.user_id = po.user_id
      AND d.status IN ('confirmed', 'pending')
      AND d.amount = po.token_amount
      AND d.created_at > po.created_at - INTERVAL '1 hour'
      AND d.created_at < po.created_at + INTERVAL '2 hours'
  );

-- Reset stuck PAJ onramp deposits where the old code claimed the order
-- (set deposit_id) but returned early without crediting because
-- used_user_wallet=true and Bridge webhook hadn't fired yet.
UPDATE paj_orders po
SET deposit_id = NULL
WHERE po.order_type = 'onramp'
  AND po.status = 'completed'
  AND po.used_user_wallet = true
  AND po.deposit_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM ledger_transactions lt
    WHERE lt.idempotency_key = 'paj-onramp-' || po.paj_order_id
  );

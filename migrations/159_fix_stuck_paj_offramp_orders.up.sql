-- Mark PAJ offramp orders as completed where Bridge confirmed delivery
-- (bridge_transfer_id is set = Bridge successfully sent the USDC)
UPDATE paj_orders
SET status = 'completed', updated_at = NOW()
WHERE order_type = 'offramp'
  AND status = 'pending'
  AND bridge_transfer_id IS NOT NULL;

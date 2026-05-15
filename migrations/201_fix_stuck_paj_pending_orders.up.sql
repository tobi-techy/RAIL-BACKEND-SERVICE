-- Fix 3 PAJ offramp orders stuck in pending that have already gone through.
UPDATE paj_orders
SET status = 'completed', updated_at = NOW()
WHERE paj_order_id IN (
    '6a01ecc05d8b6518c2cb006e',
    '69ff9d828371126223cde8a2',
    '69fdf7728371126223cd0f8e'
)
AND status NOT IN ('completed', 'failed');

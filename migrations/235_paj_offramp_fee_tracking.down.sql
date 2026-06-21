-- Safety: refuse to drop columns while swept or unswept fees exist,
-- so we don't silently re-sweep or lose revenue data.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM paj_orders WHERE fee_swept = TRUE LIMIT 1) THEN
        RAISE EXCEPTION 'cannot drop paj_orders.fee_swept while swept records exist';
    END IF;
    IF EXISTS (SELECT 1 FROM paj_orders WHERE rail_fee_usdc IS NOT NULL AND rail_fee_usdc > 0 AND fee_swept = FALSE LIMIT 1) THEN
        RAISE EXCEPTION 'cannot drop paj_orders.rail_fee_usdc while unswept fees exist - run revenue sweep first';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_paj_orders_unswept_fees;
ALTER TABLE paj_orders
    DROP COLUMN IF EXISTS fee_swept_at,
    DROP COLUMN IF EXISTS fee_swept,
    DROP COLUMN IF EXISTS rail_fee_usdc;

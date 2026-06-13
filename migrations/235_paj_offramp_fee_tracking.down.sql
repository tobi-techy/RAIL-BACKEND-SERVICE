-- Safety: refuse to drop the column once any fee has been marked swept,
-- so we don't silently re-sweep the same fees on a re-up.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM paj_orders WHERE fee_swept = TRUE LIMIT 1) THEN
        RAISE EXCEPTION 'cannot drop paj_orders.fee_swept while swept records exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_paj_orders_unswept_fees;
ALTER TABLE paj_orders
    DROP COLUMN IF EXISTS fee_swept_at,
    DROP COLUMN IF EXISTS fee_swept,
    DROP COLUMN IF EXISTS rail_fee_usdc;

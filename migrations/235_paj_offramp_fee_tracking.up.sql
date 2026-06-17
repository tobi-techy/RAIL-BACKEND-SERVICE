-- Track Rail's USDC fee on Paj offramps so the revenue sweep worker can move
-- the fee out of the user's Circle wallet into the treasury wallet.
-- Until this is in place, the fee is debited in the ledger (credited to the
-- withdrawal_fee_revenue account) but the USDC physically remains in the
-- user's wallet, leaking platform revenue on every NGN withdrawal.

ALTER TABLE paj_orders
    ADD COLUMN IF NOT EXISTS rail_fee_usdc DECIMAL(20, 8),
    ADD COLUMN IF NOT EXISTS fee_swept     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS fee_swept_at  TIMESTAMPTZ;

-- Index to keep the sweep scan cheap.
CREATE INDEX IF NOT EXISTS idx_paj_orders_unswept_fees
    ON paj_orders (created_at)
    WHERE order_type = 'offramp'
      AND status = 'completed'
      AND rail_fee_usdc IS NOT NULL
      AND fee_swept = FALSE;

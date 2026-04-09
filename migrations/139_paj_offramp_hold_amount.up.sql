-- Add hold_amount column to track the exact USDC amount debited from the user's
-- spending balance for offramp orders (includes slippage buffer).
-- This ensures failure reversals refund the correct amount.
ALTER TABLE paj_orders ADD COLUMN IF NOT EXISTS hold_amount DECIMAL(20, 8);

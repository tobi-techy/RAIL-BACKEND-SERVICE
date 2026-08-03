-- Travel orders move from the Travu aggregator to BRIJ Travel (travel.brij.fi).
-- BRIJ is settled per call with x402 micropayments in USDC on Solana mainnet;
-- every booking is an escrow-backed intent paid from Rail's funding wallet,
-- with the user charged via a ledger hold. Travu-specific columns are dropped
-- and replaced with the BRIJ intent/escrow bookkeeping.
ALTER TABLE travel_orders DROP COLUMN IF EXISTS travu_order_id;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS travu_order_number;

-- BRIJ intent lifecycle identifiers.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS intent_id VARCHAR(128);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS offer_id VARCHAR(128);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS customer_support_code VARCHAR(32);

-- Escrow bookkeeping (amounts in USDC, 6 decimals on-chain).
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS expected_escrow_amount DECIMAL(20, 8);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS escrow_amount_usdc DECIMAL(20, 8);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS escrow_mint VARCHAR(64);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS escrow_address VARCHAR(64);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS escrow_funded_at TIMESTAMP WITH TIME ZONE;

-- Airline order + ticket reference.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS airline_order_id VARCHAR(128);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS order_ref VARCHAR(128);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS booking_reference VARCHAR(64);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS ticketed_at TIMESTAMP WITH TIME ZONE;

-- Refund state.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refund_reason VARCHAR(1000);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_travel_orders_intent ON travel_orders(intent_id);
CREATE INDEX IF NOT EXISTS idx_travel_orders_booked ON travel_orders(status, updated_at) WHERE status = 'booked';

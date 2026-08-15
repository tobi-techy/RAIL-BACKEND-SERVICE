DROP INDEX IF EXISTS idx_travel_orders_booked;
DROP INDEX IF EXISTS idx_travel_orders_intent;

ALTER TABLE travel_orders DROP COLUMN IF EXISTS refunded_at;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS refund_reason;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS ticketed_at;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS booking_reference;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS order_ref;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS airline_order_id;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS escrow_funded_at;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS escrow_address;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS escrow_mint;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS escrow_amount_usdc;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS expected_escrow_amount;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS customer_support_code;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS offer_id;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS intent_id;

-- Restore the Travu columns dropped in the forward migration.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS travu_order_id VARCHAR(128);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS travu_order_number VARCHAR(128);

DROP INDEX IF EXISTS idx_travel_orders_refund_pending;

ALTER TABLE travel_orders DROP COLUMN IF EXISTS refund_credited_at;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS refund_contact;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS refund_status;
ALTER TABLE travel_orders DROP COLUMN IF EXISTS refund_requested_at;

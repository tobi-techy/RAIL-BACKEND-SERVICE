-- Refund tracking for BRIJ flight bookings. BRIJ refunds are manual (each
-- request is reviewed), so the recovery worker needs to know a refund was
-- filed, poll the intent until it settles, and then credit the user's ledger
-- hold automatically. refund_status is 'requested' while pending and becomes
-- 'approved' once the credit has been posted.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refund_requested_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refund_status VARCHAR(32);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refund_contact VARCHAR(256);
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS refund_credited_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_travel_orders_refund_pending
  ON travel_orders(status, updated_at)
  WHERE refund_status = 'requested';

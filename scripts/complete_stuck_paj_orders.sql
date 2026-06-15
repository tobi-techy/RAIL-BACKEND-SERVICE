-- Migration: Complete stuck PAJ offramp orders that have been pending > 1 hour
-- Run this ONLY if you've confirmed with PAJ that the NGN was delivered to users' banks.
-- If unsure, use 'failed' instead of 'completed' and the recovery worker will refund users.

BEGIN;

-- Show what will be affected
SELECT paj_order_id, user_id, fiat_amount, bank_account_name, bank_account_number, status, created_at
FROM paj_orders
WHERE order_type = 'offramp'
  AND status IN ('pending', 'processing')
  AND created_at < NOW() - interval '1 hour';

-- Mark as completed
UPDATE paj_orders
SET status = 'completed',
    last_webhook_status = 'manual-completed:admin-script',
    updated_at = NOW()
WHERE order_type = 'offramp'
  AND status IN ('pending', 'processing')
  AND created_at < NOW() - interval '1 hour';

COMMIT;

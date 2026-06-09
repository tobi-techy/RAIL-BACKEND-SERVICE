-- Fix stuck transactions: update all 'pending' transactions whose linked
-- deposits have reached a terminal success state (confirmed, off_ramp_completed, broker_funded).
-- Also update withdrawals stuck in 'processing' that already completed at the provider level.

-- 1) Deposits that completed but transactions table still says 'pending'
UPDATE transactions
SET status = 'processed', processed_at = NOW(), updated_at = NOW()
WHERE status = 'pending'
  AND type = 'deposit'
  AND id IN (
    SELECT t.id FROM transactions t
    JOIN deposits d ON d.user_id = t.user_id
      AND d.amount = t.amount
      AND d.status IN ('confirmed', 'off_ramp_completed', 'broker_funded')
    WHERE t.status = 'pending' AND t.type = 'deposit'
  );

-- 2) Withdrawals that completed but transactions table still says 'pending'
UPDATE transactions
SET status = 'processed', processed_at = NOW(), updated_at = NOW()
WHERE status = 'pending'
  AND type = 'withdrawal'
  AND id IN (
    SELECT t.id FROM transactions t
    JOIN withdrawals w ON w.user_id = t.user_id AND w.amount = t.amount
      AND w.status = 'completed'
    WHERE t.status = 'pending' AND t.type = 'withdrawal'
  );

-- 3) Direct fix: mark all withdrawals with 'completed' status in withdrawals table
-- so they display correctly in activity feed
UPDATE withdrawals
SET status = 'completed', updated_at = NOW(), completed_at = COALESCE(completed_at, NOW())
WHERE status = 'processing'
  AND provider_transfer_id IS NOT NULL
  AND created_at < NOW() - INTERVAL '1 hour';

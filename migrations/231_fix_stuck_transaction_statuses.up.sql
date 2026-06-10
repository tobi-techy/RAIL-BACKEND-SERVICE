-- Fix stuck transactions: update all 'pending' transactions whose linked
-- deposits have reached a terminal success state (confirmed, off_ramp_completed, broker_funded).
-- Also update withdrawals stuck in 'processing' that already completed at the provider level.
--
-- NOTE: Ideally these joins would use foreign keys (deposit_id/withdrawal_id on transactions).
-- Amount+user+timestamp matching is a workaround until FK relationships are added.

-- 1) Deposits that completed but transactions table still says 'pending'
UPDATE transactions
SET status = 'processed', processed_at = NOW(), updated_at = NOW()
WHERE status = 'pending'
  AND type = 'deposit'
  AND id IN (
    SELECT DISTINCT ON (t.id) t.id FROM transactions t
    JOIN deposits d ON d.user_id = t.user_id
      AND d.amount = t.amount
      AND d.status IN ('confirmed', 'off_ramp_completed', 'broker_funded')
      AND ABS(EXTRACT(EPOCH FROM (d.created_at - t.created_at))) < 300
    WHERE t.status = 'pending' AND t.type = 'deposit'
    ORDER BY t.id, ABS(EXTRACT(EPOCH FROM (d.created_at - t.created_at)))
  );

-- 2) Withdrawals that completed but transactions table still says 'pending'
UPDATE transactions
SET status = 'processed', processed_at = NOW(), updated_at = NOW()
WHERE status = 'pending'
  AND type = 'withdrawal'
  AND id IN (
    SELECT DISTINCT ON (t.id) t.id FROM transactions t
    JOIN withdrawals w ON w.user_id = t.user_id AND w.amount = t.amount
      AND w.status = 'completed'
      AND ABS(EXTRACT(EPOCH FROM (w.created_at - t.created_at))) < 300
    WHERE t.status = 'pending' AND t.type = 'withdrawal'
    ORDER BY t.id, ABS(EXTRACT(EPOCH FROM (w.created_at - t.created_at)))
  );

-- 3) Direct fix: mark withdrawals as completed if provider confirmed transfer.
-- NOTE: Only safe for withdrawals with a provider_transfer_id (confirms provider processed it).
UPDATE withdrawals
SET status = 'completed', updated_at = NOW(), completed_at = COALESCE(completed_at, NOW())
WHERE status = 'processing'
  AND provider_transfer_id IS NOT NULL
  AND created_at < NOW() - INTERVAL '1 hour';

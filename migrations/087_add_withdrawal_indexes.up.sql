-- Add missing indexes for withdrawal query performance
-- Composite index for GetPendingWithdrawalsTotal queries
CREATE INDEX IF NOT EXISTS idx_withdrawals_user_status ON withdrawals(user_id, status);

-- Index for sorting by created_at
CREATE INDEX IF NOT EXISTS idx_withdrawals_created_at ON withdrawals(created_at);

-- Index for stuck withdrawal queries (updated_at)
CREATE INDEX IF NOT EXISTS idx_withdrawals_updated_at ON withdrawals(updated_at);

-- Composite index for user + status + created_at (common query pattern)
CREATE INDEX IF NOT EXISTS idx_withdrawals_user_status_created ON withdrawals(user_id, status, created_at DESC);

-- Add idempotency_key column for duplicate prevention
ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255) UNIQUE;

-- Index for idempotency lookups
CREATE INDEX IF NOT EXISTS idx_withdrawals_idempotency_key ON withdrawals(idempotency_key);

ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS fee_swept BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_withdrawals_unswept_fees ON withdrawals (created_at) WHERE status = 'completed' AND fee_amount > 0 AND fee_swept = FALSE;

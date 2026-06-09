DROP INDEX IF EXISTS idx_withdrawals_unswept_fees;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS fee_swept;

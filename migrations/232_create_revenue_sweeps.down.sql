-- Safety: prevent rollback if fees have already been swept (would cause duplicates on re-apply)
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM withdrawals WHERE fee_swept = TRUE LIMIT 1) THEN
    RAISE EXCEPTION 'Cannot rollback: withdrawals have been fee-swept. Manual intervention required to prevent duplicate transfers.';
END IF;
END $$;

DROP INDEX IF EXISTS idx_withdrawals_unswept_fees;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS fee_swept;

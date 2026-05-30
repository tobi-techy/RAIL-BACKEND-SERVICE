-- Prevent duplicate transaction rows on job re-delivery
-- First remove any existing duplicates (keep the earliest inserted row)
DELETE FROM bank_statement_transactions a USING bank_statement_transactions b
WHERE a.id > b.id
  AND a.upload_id = b.upload_id
  AND a.transaction_date = b.transaction_date
  AND a.amount = b.amount
  AND a.type = b.type
  AND a.description = b.description;

-- Unique index (with partial condition for non-null balance_after) prevents
-- false-positive dedup while still catching true duplicates.
DROP INDEX IF EXISTS uq_bank_stmt_txns_dedup;
ALTER TABLE bank_statement_transactions DROP CONSTRAINT IF EXISTS uq_bank_stmt_txns_dedup;
CREATE UNIQUE INDEX uq_bank_stmt_txns_dedup
    ON bank_statement_transactions(upload_id, transaction_date, amount, type, description, balance_after)
    WHERE balance_after IS NOT NULL;

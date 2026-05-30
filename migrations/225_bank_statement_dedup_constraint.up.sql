-- Prevent duplicate transaction rows on job re-delivery
-- First remove any existing duplicates (keep the earliest row per unique set)
DELETE FROM bank_statement_transactions a USING bank_statement_transactions b
WHERE a.ctid < b.ctid
  AND a.upload_id = b.upload_id
  AND a.transaction_date = b.transaction_date
  AND a.amount = b.amount
  AND a.type = b.type
  AND a.description = b.description;

ALTER TABLE bank_statement_transactions ADD CONSTRAINT uq_bank_stmt_txns_dedup
    UNIQUE (upload_id, transaction_date, amount, type, description);

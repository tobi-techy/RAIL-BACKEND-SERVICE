DROP INDEX IF EXISTS uq_bank_stmt_txns_dedup;
ALTER TABLE bank_statement_transactions ADD CONSTRAINT uq_bank_stmt_txns_dedup
    UNIQUE (upload_id, transaction_date, amount, type, description);

DROP TABLE IF EXISTS statement_category_rules;

DROP INDEX IF EXISTS idx_bank_stmt_txns_user_recurrence;

ALTER TABLE bank_statement_transactions
    DROP CONSTRAINT IF EXISTS bank_statement_transactions_recurrence_check;

ALTER TABLE bank_statement_transactions
    DROP COLUMN IF EXISTS recurrence,
    DROP COLUMN IF EXISTS category_confidence,
    DROP COLUMN IF EXISTS is_essential,
    DROP COLUMN IF EXISTS counterparty;

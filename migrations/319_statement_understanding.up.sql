-- Structured understanding of each statement line, plus user corrections.
-- Existing rows keep a confidence of 0.700 so historical advice does not go blank.
-- New lines written by the parser set their own confidence.

ALTER TABLE bank_statement_transactions
    ADD COLUMN IF NOT EXISTS counterparty TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS is_essential BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS category_confidence NUMERIC(4,3) NOT NULL DEFAULT 0.700,
    ADD COLUMN IF NOT EXISTS recurrence TEXT NOT NULL DEFAULT 'one_off';

ALTER TABLE bank_statement_transactions
    DROP CONSTRAINT IF EXISTS bank_statement_transactions_recurrence_check;
ALTER TABLE bank_statement_transactions
    ADD CONSTRAINT bank_statement_transactions_recurrence_check
    CHECK (recurrence IN ('one_off', 'bill', 'subscription'));

CREATE INDEX IF NOT EXISTS idx_bank_stmt_txns_user_recurrence
    ON bank_statement_transactions(user_id, recurrence);

CREATE TABLE IF NOT EXISTS statement_category_rules (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    match_type TEXT NOT NULL CHECK (match_type IN ('exact', 'contains')),
    pattern TEXT NOT NULL,
    bucket TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, match_type, pattern)
);

CREATE INDEX IF NOT EXISTS idx_statement_category_rules_user
    ON statement_category_rules(user_id);

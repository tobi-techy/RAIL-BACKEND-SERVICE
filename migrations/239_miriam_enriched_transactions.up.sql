CREATE TABLE IF NOT EXISTS miriam_enriched_transactions (
    id UUID PRIMARY KEY,
    transaction_id UUID NOT NULL,
    user_id UUID NOT NULL,
    raw_description TEXT NOT NULL,
    amount NUMERIC(20,8) NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    transaction_date TIMESTAMPTZ NOT NULL,
    direction VARCHAR(10) NOT NULL,
    counterparty TEXT NOT NULL,
    category_l1 VARCHAR(50) NOT NULL DEFAULT 'Uncategorized',
    category_l2 VARCHAR(50) NOT NULL DEFAULT 'Other',
    is_essential BOOLEAN NOT NULL DEFAULT FALSE,
    is_recurring BOOLEAN NOT NULL DEFAULT FALSE,
    classification_layer VARCHAR(10) NOT NULL,
    confidence NUMERIC(5,4) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_enriched_txn_transaction_id ON miriam_enriched_transactions(transaction_id);
CREATE INDEX idx_enriched_txn_user_id ON miriam_enriched_transactions(user_id);
CREATE INDEX idx_enriched_txn_user_category ON miriam_enriched_transactions(user_id, category_l1);
CREATE INDEX idx_enriched_txn_user_date ON miriam_enriched_transactions(user_id, transaction_date DESC);

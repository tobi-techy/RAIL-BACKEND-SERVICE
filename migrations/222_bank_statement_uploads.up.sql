-- Bank statement uploads tracking
CREATE TABLE bank_statement_uploads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bank_name TEXT NOT NULL,
    file_hash TEXT NOT NULL,
    file_size_bytes INTEGER NOT NULL,
    page_count INTEGER,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    error_message TEXT,
    period_start DATE,
    period_end DATE,
    transaction_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_bank_statement_uploads_user_hash ON bank_statement_uploads(user_id, file_hash);
CREATE INDEX idx_bank_statement_uploads_user_id ON bank_statement_uploads(user_id);
CREATE INDEX idx_bank_statement_uploads_status ON bank_statement_uploads(user_id, status);

-- Individual parsed transactions from bank statements
CREATE TABLE bank_statement_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    upload_id UUID NOT NULL REFERENCES bank_statement_uploads(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    transaction_date DATE NOT NULL,
    description TEXT NOT NULL,
    amount NUMERIC(18,2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'NGN',
    type TEXT NOT NULL CHECK (type IN ('credit', 'debit')),
    category TEXT NOT NULL DEFAULT 'uncategorized',
    balance_after NUMERIC(18,2),
    raw_line TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_bank_stmt_txns_upload ON bank_statement_transactions(upload_id);
CREATE INDEX idx_bank_stmt_txns_user_date ON bank_statement_transactions(user_id, transaction_date DESC);
CREATE INDEX idx_bank_stmt_txns_user_category ON bank_statement_transactions(user_id, category);

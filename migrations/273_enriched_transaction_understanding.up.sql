-- Add understanding fields to enriched transactions for Miriam to read transactions like plain text.
ALTER TABLE miriam_enriched_transactions
    ADD COLUMN IF NOT EXISTS plain_description TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS merchant_context TEXT NOT NULL DEFAULT '';

-- Index for batch lookup by user + transaction IDs (used by spending tools).
CREATE INDEX IF NOT EXISTS idx_enriched_txn_user_id ON miriam_enriched_transactions(user_id);

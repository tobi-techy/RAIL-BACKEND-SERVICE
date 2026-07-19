-- Add bank and tx_type columns from ML sidecar output.
ALTER TABLE miriam_enriched_transactions
    ADD COLUMN IF NOT EXISTS bank TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tx_type TEXT NOT NULL DEFAULT '';

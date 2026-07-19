ALTER TABLE miriam_enriched_transactions
    DROP COLUMN IF EXISTS bank,
    DROP COLUMN IF EXISTS tx_type;

ALTER TABLE miriam_enriched_transactions
    DROP COLUMN IF EXISTS plain_description,
    DROP COLUMN IF EXISTS merchant_context;

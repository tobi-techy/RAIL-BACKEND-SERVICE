ALTER TABLE miriam_enriched_transactions
    DROP COLUMN IF EXISTS behavior_tags,
    DROP COLUMN IF EXISTS facts,
    DROP COLUMN IF EXISTS embedding;

DROP INDEX IF EXISTS idx_enriched_txn_behavior_tags;
DROP INDEX IF EXISTS idx_enriched_txn_facts;

-- Add behavior_tags, facts, and embedding columns for the full enrichment pipeline.
-- behavior_tags: JSON array of detected behavioral patterns (subscriptions, gambling, etc.)
-- facts: JSON array of durable financial facts for Miriam's memory
-- embedding: vector for semantic similarity search (128-dim hash fallback or 384-dim sentence-transformer)

ALTER TABLE miriam_enriched_transactions
    ADD COLUMN IF NOT EXISTS behavior_tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS facts JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS embedding REAL[] NOT NULL DEFAULT '{}';

-- GIN index for behavior_tags queries (e.g. find all transactions with gambling tag)
CREATE INDEX IF NOT EXISTS idx_enriched_txn_behavior_tags ON miriam_enriched_transactions USING GIN (behavior_tags);

-- GIN index for facts queries (e.g. find all transactions with recurring_expense fact)
CREATE INDEX IF NOT EXISTS idx_enriched_txn_facts ON miriam_enriched_transactions USING GIN (facts);

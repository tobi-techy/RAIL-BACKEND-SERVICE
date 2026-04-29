-- Miriam Memory Improvements: embeddings, summaries, staleness

-- 1. Add embedding vector to facts for semantic dedup
ALTER TABLE miriam_user_facts ADD COLUMN IF NOT EXISTS embedding vector(768);

-- 2. Cross-conversation memory summary (compressed narrative per user)
CREATE TABLE IF NOT EXISTS miriam_memory_summaries (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    summary TEXT NOT NULL DEFAULT '',
    fact_count INTEGER NOT NULL DEFAULT 0,
    last_summarized_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Switch knowledge embeddings from OpenAI 1536-dim to Gemini 768-dim.
-- Truncate existing data since embeddings from different models are incompatible.
TRUNCATE TABLE knowledge_embeddings;
DROP INDEX IF EXISTS idx_knowledge_embeddings_hnsw;
ALTER TABLE knowledge_embeddings ALTER COLUMN embedding TYPE vector(768);
CREATE INDEX idx_knowledge_embeddings_hnsw ON knowledge_embeddings USING hnsw (embedding vector_cosine_ops);

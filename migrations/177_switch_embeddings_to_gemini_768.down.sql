-- Revert to OpenAI 1536-dim embeddings.
TRUNCATE TABLE knowledge_embeddings;
DROP INDEX IF EXISTS idx_knowledge_embeddings_hnsw;
ALTER TABLE knowledge_embeddings ALTER COLUMN embedding TYPE vector(1536);
CREATE INDEX idx_knowledge_embeddings_hnsw ON knowledge_embeddings USING hnsw (embedding vector_cosine_ops);

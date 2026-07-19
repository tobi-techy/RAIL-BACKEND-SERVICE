-- Document intelligence pipeline: top-level document records.
-- One row per uploaded file (image or PDF). Original bytes live in object
-- storage (R2/S3); this table stores only metadata + the storage key.
CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed', 'failed')),
    type TEXT NOT NULL DEFAULT 'unknown' CHECK (type IN ('unknown', 'receipt', 'bank_statement', 'invoice')),
    file_key TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    file_size_bytes INTEGER NOT NULL,
    file_hash TEXT NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Dedup: the same file uploaded twice by a user maps to one document.
CREATE UNIQUE INDEX uq_documents_user_hash ON documents(user_id, file_hash);
CREATE INDEX idx_documents_user_id ON documents(user_id, created_at DESC);
CREATE INDEX idx_documents_status ON documents(status);

-- Validation outcome per document. Records whether deterministic checks
-- (receipt math, opening + credits - debits = closing) passed, the specific
-- errors found, and a 0-1 confidence used to gate OCR retries and LLM calls.
CREATE TABLE document_validation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    passed BOOLEAN NOT NULL DEFAULT FALSE,
    errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_document_validation_document ON document_validation(document_id);

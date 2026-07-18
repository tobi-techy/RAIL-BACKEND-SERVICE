-- Raw OCR output per document. Stores the full reconstructed text plus the
-- per-line results (text + bbox + confidence) as JSONB for later layout work.
CREATE TABLE ocr_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    raw_text TEXT NOT NULL DEFAULT '',
    mean_confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    engine TEXT NOT NULL DEFAULT 'paddleocr',
    page_count INTEGER NOT NULL DEFAULT 0,
    lines JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_ocr_results_document ON ocr_results(document_id);

-- Deterministically extracted fields per document. Common receipt/statement
-- fields are promoted to columns for querying; the full structured payload
-- (line items, transaction arrays, etc.) is kept in the json column.
CREATE TABLE extracted_fields (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    merchant TEXT,
    amount NUMERIC(18,2),
    currency TEXT,
    doc_date DATE,
    account_name TEXT,
    opening_balance NUMERIC(18,2),
    closing_balance NUMERIC(18,2),
    json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_extracted_fields_document ON extracted_fields(document_id);

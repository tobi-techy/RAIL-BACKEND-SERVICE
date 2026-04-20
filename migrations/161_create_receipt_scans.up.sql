-- Receipt scans: structured data extracted from receipt images via GPT-4o vision.
CREATE TABLE IF NOT EXISTS receipt_scans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    merchant VARCHAR(255) NOT NULL,
    amount DECIMAL(20, 2) NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    receipt_date DATE,
    category VARCHAR(100) NOT NULL DEFAULT 'Uncategorized',
    items JSONB DEFAULT '[]',
    raw_text TEXT,
    image_hash VARCHAR(64),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_receipt_scans_user ON receipt_scans(user_id, created_at DESC);
CREATE INDEX idx_receipt_scans_category ON receipt_scans(user_id, category);

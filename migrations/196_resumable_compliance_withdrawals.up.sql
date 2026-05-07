ALTER TABLE withdrawals
    ADD COLUMN IF NOT EXISTS source_chain VARCHAR(50),
    ADD COLUMN IF NOT EXISTS source_wallet_address VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_wallet_type VARCHAR(20),
    ADD COLUMN IF NOT EXISTS emergency BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_withdrawals_compliance_review
    ON withdrawals(status, updated_at)
    WHERE status = 'compliance_review';

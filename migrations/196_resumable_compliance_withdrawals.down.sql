DROP INDEX IF EXISTS idx_withdrawals_compliance_review;

ALTER TABLE withdrawals
    DROP COLUMN IF EXISTS emergency,
    DROP COLUMN IF EXISTS provider_wallet_type,
    DROP COLUMN IF EXISTS source_wallet_address,
    DROP COLUMN IF EXISTS source_chain;

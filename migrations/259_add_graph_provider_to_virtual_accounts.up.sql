-- Add Graph (useoval.com) provider support to virtual_accounts.
-- Bridge remains the default provider; Graph is used for NGN named accounts.
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS provider VARCHAR(20) NOT NULL DEFAULT 'bridge';
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS graph_person_id VARCHAR(64);
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS graph_account_id VARCHAR(64);
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS bank_code VARCHAR(20) DEFAULT '';

-- bridge_customer_id is Bridge-specific; Graph accounts don't have one.
ALTER TABLE virtual_accounts ALTER COLUMN bridge_customer_id DROP NOT NULL;

-- One Graph bank account maps to exactly one row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_virtual_accounts_graph_account_id
    ON virtual_accounts(graph_account_id)
    WHERE graph_account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_virtual_accounts_provider ON virtual_accounts(provider);

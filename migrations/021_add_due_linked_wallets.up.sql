-- Migration: Add due_linked_wallets table to track Circle wallets linked to Due accounts
-- Purpose: Before an account can move money to/from blockchain wallets via Due, wallets must be linked
--          This enables Due to monitor and screen wallets for compliance

CREATE TABLE IF NOT EXISTS due_linked_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    due_account_id VARCHAR(255) NOT NULL REFERENCES due_accounts(due_id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    managed_wallet_id UUID NOT NULL REFERENCES managed_wallets(id) ON DELETE CASCADE,
    
    -- Due API wallet data
    due_wallet_id VARCHAR(255) NOT NULL UNIQUE,
    wallet_address TEXT NOT NULL,
    formatted_address TEXT NOT NULL, -- e.g., "evm:0x123..." as required by Due API
    blockchain VARCHAR(50) NOT NULL, -- ETH-SEPOLIA, MATIC-AMOY, BASE-SEPOLIA, etc.
    
    -- Status tracking
    status VARCHAR(50) NOT NULL DEFAULT 'linked', -- linked, monitoring, suspended, unlinked
    
    -- Metadata
    linked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_monitored_at TIMESTAMPTZ,
    compliance_checked_at TIMESTAMPTZ,
    
    -- Audit fields
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    -- Constraints
    CONSTRAINT due_linked_wallets_status_check CHECK (status IN ('linked', 'monitoring', 'suspended', 'unlinked')),
    CONSTRAINT due_linked_wallets_unique_managed_wallet UNIQUE (managed_wallet_id),
    CONSTRAINT due_linked_wallets_unique_due_wallet UNIQUE (due_wallet_id)
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_due_linked_wallets_due_account ON due_linked_wallets(due_account_id);
CREATE INDEX IF NOT EXISTS idx_due_linked_wallets_user ON due_linked_wallets(user_id);
CREATE INDEX IF NOT EXISTS idx_due_linked_wallets_managed_wallet ON due_linked_wallets(managed_wallet_id);
CREATE INDEX IF NOT EXISTS idx_due_linked_wallets_status ON due_linked_wallets(status);
CREATE INDEX IF NOT EXISTS idx_due_linked_wallets_blockchain ON due_linked_wallets(blockchain);

-- Comments
COMMENT ON TABLE due_linked_wallets IS 'Tracks Circle developer-controlled wallets linked to Due accounts for compliance monitoring';
COMMENT ON COLUMN due_linked_wallets.due_account_id IS 'Reference to the Due account ID (from due_accounts.due_id)';
COMMENT ON COLUMN due_linked_wallets.managed_wallet_id IS 'Reference to the Circle managed wallet';
COMMENT ON COLUMN due_linked_wallets.due_wallet_id IS 'Due wallet ID returned from /v1/wallets endpoint';
COMMENT ON COLUMN due_linked_wallets.formatted_address IS 'Address formatted for Due API (e.g., evm:0x...)';
COMMENT ON COLUMN due_linked_wallets.status IS 'Current status of the linked wallet';
COMMENT ON COLUMN due_linked_wallets.linked_at IS 'Timestamp when wallet was successfully linked to Due account';

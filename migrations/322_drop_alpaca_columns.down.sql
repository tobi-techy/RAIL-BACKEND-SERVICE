-- Rollback of 322: restore the vestigial Alpaca columns and the instant funding table.
-- Mirrors migrations/019, 023, 084 and 015.

ALTER TABLE users ADD COLUMN IF NOT EXISTS alpaca_account_id VARCHAR(255);
CREATE INDEX IF NOT EXISTS idx_users_alpaca_account_id ON users(alpaca_account_id) WHERE alpaca_account_id IS NOT NULL;

ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS alpaca_account_id VARCHAR(100);
CREATE INDEX IF NOT EXISTS idx_virtual_accounts_alpaca_account_id ON virtual_accounts(alpaca_account_id);

ALTER TABLE deposits ADD COLUMN IF NOT EXISTS alpaca_funding_tx_id VARCHAR(100);
ALTER TABLE deposits ADD COLUMN IF NOT EXISTS alpaca_funded_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_deposits_alpaca_funding_tx_id ON deposits(alpaca_funding_tx_id);

CREATE TABLE IF NOT EXISTS instant_fundings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    alpaca_account_id VARCHAR(255) NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    journal_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMP WITH TIME ZONE,

    CONSTRAINT instant_fundings_status_check CHECK (status IN ('active', 'settled', 'repaid'))
);

CREATE INDEX IF NOT EXISTS idx_instant_fundings_user_id ON instant_fundings(user_id);
CREATE INDEX IF NOT EXISTS idx_instant_fundings_user_status ON instant_fundings(user_id, status);
CREATE INDEX IF NOT EXISTS idx_instant_fundings_created_at ON instant_fundings(created_at DESC);

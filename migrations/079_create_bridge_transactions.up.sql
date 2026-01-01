-- Bridge transactions for CCTP cross-chain USDC transfers
CREATE TABLE bridge_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    source_chain VARCHAR(16) NOT NULL,
    dest_chain VARCHAR(16) NOT NULL DEFAULT 'SOL',
    amount DECIMAL(20,8) NOT NULL,
    source_tx_hash VARCHAR(128),
    message_hash VARCHAR(128),
    attestation TEXT,
    dest_tx_hash VARCHAR(128),
    dest_address VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    error_message TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_bridge_transactions_user_id ON bridge_transactions(user_id);
CREATE INDEX idx_bridge_transactions_status ON bridge_transactions(status);
CREATE INDEX idx_bridge_transactions_source_tx ON bridge_transactions(source_tx_hash);

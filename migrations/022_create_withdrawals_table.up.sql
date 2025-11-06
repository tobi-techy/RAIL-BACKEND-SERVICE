-- Create withdrawals table for tracking USD to USDC withdrawal flow
CREATE TABLE IF NOT EXISTS withdrawals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    alpaca_account_id VARCHAR(255) NOT NULL,
    amount DECIMAL(20, 8) NOT NULL,
    destination_chain VARCHAR(50) NOT NULL,
    destination_address VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    alpaca_journal_id VARCHAR(255),
    due_transfer_id VARCHAR(255),
    due_recipient_id VARCHAR(255),
    tx_hash VARCHAR(255),
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP
);

CREATE INDEX idx_withdrawals_user_id ON withdrawals(user_id);
CREATE INDEX idx_withdrawals_status ON withdrawals(status);
CREATE INDEX idx_withdrawals_alpaca_journal_id ON withdrawals(alpaca_journal_id);
CREATE INDEX idx_withdrawals_due_transfer_id ON withdrawals(due_transfer_id);

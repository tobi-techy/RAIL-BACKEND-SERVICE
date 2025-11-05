-- Create virtual_accounts table for Due virtual account integration
CREATE TABLE virtual_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    due_account_id VARCHAR(100) NOT NULL UNIQUE,
    alpaca_account_id VARCHAR(100) NOT NULL,
    account_number VARCHAR(50) NOT NULL UNIQUE,
    routing_number VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'closed', 'failed')),
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, alpaca_account_id)
);

-- Create indexes for virtual_accounts
CREATE INDEX idx_virtual_accounts_user_id ON virtual_accounts(user_id);
CREATE INDEX idx_virtual_accounts_due_account_id ON virtual_accounts(due_account_id);
CREATE INDEX idx_virtual_accounts_alpaca_account_id ON virtual_accounts(alpaca_account_id);
CREATE INDEX idx_virtual_accounts_status ON virtual_accounts(status);

-- Add trigger for updated_at
CREATE TRIGGER update_virtual_accounts_updated_at 
    BEFORE UPDATE ON virtual_accounts 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();
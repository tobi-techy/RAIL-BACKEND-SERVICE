-- Migration: Create Ledger Tables for Double-Entry Bookkeeping
-- Purpose: Establish ledger as single source of truth for all financial state

-- Enable UUID extension if not already enabled
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Ledger Accounts Table
-- Represents all financial accounts (user and system level)
CREATE TABLE IF NOT EXISTS ledger_accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    account_type VARCHAR(50) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    balance DECIMAL(36, 18) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Constraints
    CONSTRAINT chk_account_type CHECK (account_type IN (
        'usdc_balance',           -- User's available USDC
        'fiat_exposure',          -- User's buying power at Alpaca (USD)
        'pending_investment',     -- User's reserved funds for in-flight trades
        'system_buffer_usdc',     -- System on-chain USDC reserve
        'system_buffer_fiat',     -- System operational USD buffer
        'broker_operational'      -- Pre-funded cash at Alpaca
    )),
    CONSTRAINT chk_currency CHECK (currency IN ('USDC', 'USD')),
    CONSTRAINT chk_balance_positive CHECK (balance >= 0)
);

-- Unique constraint: One account per user per account_type (for user accounts)
-- System accounts have NULL user_id
CREATE UNIQUE INDEX idx_ledger_accounts_user_type ON ledger_accounts(user_id, account_type)
    WHERE user_id IS NOT NULL;

-- Unique constraint: One system account per account_type
CREATE UNIQUE INDEX idx_ledger_accounts_system_type ON ledger_accounts(account_type)
    WHERE user_id IS NULL AND account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational');

-- Performance indexes
CREATE INDEX idx_ledger_accounts_user_id ON ledger_accounts(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_ledger_accounts_type ON ledger_accounts(account_type);
CREATE INDEX idx_ledger_accounts_currency ON ledger_accounts(currency);

-- Ledger Transactions Table
-- Groups related ledger entries (always contains exactly 2 entries: debit + credit)
CREATE TABLE IF NOT EXISTS ledger_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    transaction_type VARCHAR(50) NOT NULL,
    reference_id UUID,
    reference_type VARCHAR(50),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    idempotency_key VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    
    -- Constraints
    CONSTRAINT chk_transaction_type CHECK (transaction_type IN (
        'deposit',               -- USDC deposit from user
        'withdrawal',            -- USDC withdrawal to user
        'investment',            -- Trade execution
        'conversion',            -- USDC<->USD conversion
        'internal_transfer',     -- Between user accounts
        'buffer_replenishment',  -- System buffer refill
        'reversal'               -- Compensating transaction
    )),
    CONSTRAINT chk_status CHECK (status IN ('pending', 'completed', 'reversed', 'failed'))
);

-- Performance indexes
CREATE INDEX idx_ledger_transactions_user_id ON ledger_transactions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_ledger_transactions_type ON ledger_transactions(transaction_type);
CREATE INDEX idx_ledger_transactions_status ON ledger_transactions(status);
CREATE INDEX idx_ledger_transactions_reference ON ledger_transactions(reference_id, reference_type);
CREATE INDEX idx_ledger_transactions_created_at ON ledger_transactions(created_at DESC);
CREATE INDEX idx_ledger_transactions_idempotency ON ledger_transactions(idempotency_key);

-- Ledger Entries Table
-- Individual debit/credit entries (immutable)
CREATE TABLE IF NOT EXISTS ledger_entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    transaction_id UUID NOT NULL REFERENCES ledger_transactions(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES ledger_accounts(id) ON DELETE RESTRICT,
    entry_type VARCHAR(10) NOT NULL,
    amount DECIMAL(36, 18) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    description TEXT,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Constraints
    CONSTRAINT chk_entry_type CHECK (entry_type IN ('debit', 'credit')),
    CONSTRAINT chk_amount_positive CHECK (amount >= 0),
    CONSTRAINT chk_entry_currency CHECK (currency IN ('USDC', 'USD'))
);

-- Performance indexes
CREATE INDEX idx_ledger_entries_transaction_id ON ledger_entries(transaction_id);
CREATE INDEX idx_ledger_entries_account_id ON ledger_entries(account_id);
CREATE INDEX idx_ledger_entries_created_at ON ledger_entries(created_at DESC);
CREATE INDEX idx_ledger_entries_type ON ledger_entries(entry_type);

-- Composite index for account balance queries
CREATE INDEX idx_ledger_entries_account_created ON ledger_entries(account_id, created_at DESC);

-- Update trigger for ledger_accounts
CREATE TRIGGER update_ledger_accounts_updated_at
    BEFORE UPDATE ON ledger_accounts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Function to validate double-entry bookkeeping
-- Ensures each transaction has balanced debits and credits
CREATE OR REPLACE FUNCTION validate_ledger_balance()
RETURNS TRIGGER AS $$
DECLARE
    debit_sum DECIMAL(36, 18);
    credit_sum DECIMAL(36, 18);
    entry_count INT;
BEGIN
    -- Count entries for this transaction
    SELECT COUNT(*) INTO entry_count
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id;

    -- Transaction must have at least 2 entries
    IF entry_count < 2 THEN
        RETURN NEW;
    END IF;

    -- Calculate debit sum
    SELECT COALESCE(SUM(amount), 0) INTO debit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id
    AND entry_type = 'debit';

    -- Calculate credit sum
    SELECT COALESCE(SUM(amount), 0) INTO credit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id
    AND entry_type = 'credit';

    -- Debits must equal credits
    IF debit_sum != credit_sum THEN
        RAISE EXCEPTION 'Ledger transaction % is unbalanced: debits=%, credits=%',
            NEW.transaction_id, debit_sum, credit_sum;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to validate ledger balance after each entry insert
CREATE TRIGGER validate_ledger_entries_balance
    AFTER INSERT ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION validate_ledger_balance();

-- Create system-level accounts
-- These accounts represent operational buffers managed by the platform
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance) VALUES
    (uuid_generate_v4(), NULL, 'system_buffer_usdc', 'USDC', 0),
    (uuid_generate_v4(), NULL, 'system_buffer_fiat', 'USD', 0),
    (uuid_generate_v4(), NULL, 'broker_operational', 'USD', 0)
ON CONFLICT DO NOTHING;

-- Add comments for documentation
COMMENT ON TABLE ledger_accounts IS 'Financial accounts using double-entry bookkeeping';
COMMENT ON TABLE ledger_transactions IS 'Groups of balanced ledger entries';
COMMENT ON TABLE ledger_entries IS 'Individual debit/credit entries (immutable)';
COMMENT ON COLUMN ledger_accounts.account_type IS 'Type of account: user balances or system buffers';
COMMENT ON COLUMN ledger_transactions.idempotency_key IS 'Ensures idempotent transaction creation';
COMMENT ON COLUMN ledger_entries.entry_type IS 'Debit (increase asset/decrease liability) or Credit (decrease asset/increase liability)';

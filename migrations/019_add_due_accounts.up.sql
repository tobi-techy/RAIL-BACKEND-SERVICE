-- Add due_accounts table for storing Due API account information
-- Enables integration with Due API for account management

-- Create enum for due account status
CREATE TYPE due_account_status AS ENUM (
    'pending',
    'active',
    'suspended',
    'closed'
);

-- Create enum for due account type
CREATE TYPE due_account_type AS ENUM (
    'individual',
    'business'
);

-- Create due_accounts table
CREATE TABLE IF NOT EXISTS due_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    due_id varchar(255) NOT NULL UNIQUE,
    type due_account_type NOT NULL,
    name varchar(255) NOT NULL,
    email varchar(255) NOT NULL,
    country char(2) NOT NULL,
    category varchar(100),
    status due_account_status NOT NULL DEFAULT 'pending',
    kyc_status varchar(50) NOT NULL DEFAULT 'pending',
    tos_accepted timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- Add indexes for performance
CREATE INDEX idx_due_accounts_user_id ON due_accounts(user_id);
CREATE INDEX idx_due_accounts_due_id ON due_accounts(due_id);
CREATE INDEX idx_due_accounts_status ON due_accounts(status);
CREATE INDEX idx_due_accounts_type ON due_accounts(type);

-- Add trigger to update updated_at
CREATE OR REPLACE FUNCTION update_due_accounts_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_due_accounts_updated_at
    BEFORE UPDATE ON due_accounts
    FOR EACH ROW EXECUTE FUNCTION update_due_accounts_updated_at();

-- Enable RLS on due_accounts table
ALTER TABLE due_accounts ENABLE ROW LEVEL SECURITY;

-- RLS Policies for due_accounts
CREATE POLICY due_accounts_own ON due_accounts
    FOR ALL USING (user_id = current_user_id());

CREATE POLICY due_accounts_admin ON due_accounts
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Add constraint to ensure one due account per user
ALTER TABLE due_accounts
ADD CONSTRAINT unique_user_due_account
EXCLUDE (user_id WITH =);

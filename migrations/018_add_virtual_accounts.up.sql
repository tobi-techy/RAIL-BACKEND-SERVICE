-- Add virtual accounts table for Due API integration
-- Enables USDC to USD conversion through virtual accounts

-- Create enum for virtual account status
CREATE TYPE virtual_account_status AS ENUM (
    'creating',
    'active',
    'inactive',
    'failed'
);

-- Create virtual_accounts table
CREATE TABLE IF NOT EXISTS virtual_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    due_account_id varchar(255) NOT NULL UNIQUE,
    brokerage_account_id varchar(255),
    status virtual_account_status NOT NULL DEFAULT 'creating',
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- Add indexes for performance
CREATE INDEX idx_virtual_accounts_user_id ON virtual_accounts(user_id);
CREATE INDEX idx_virtual_accounts_status ON virtual_accounts(status);
CREATE INDEX idx_virtual_accounts_due_account_id ON virtual_accounts(due_account_id);
CREATE INDEX idx_virtual_accounts_brokerage_account_id ON virtual_accounts(brokerage_account_id);

-- Add trigger to update updated_at
CREATE OR REPLACE FUNCTION update_virtual_accounts_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_virtual_accounts_updated_at
    BEFORE UPDATE ON virtual_accounts
    FOR EACH ROW EXECUTE FUNCTION update_virtual_accounts_updated_at();

-- Enable RLS on virtual_accounts table
ALTER TABLE virtual_accounts ENABLE ROW LEVEL SECURITY;

-- RLS Policies for virtual_accounts
CREATE POLICY virtual_accounts_own ON virtual_accounts
    FOR ALL USING (user_id = current_user_id());

CREATE POLICY virtual_accounts_admin ON virtual_accounts
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Add constraint to ensure brokerage_account_id is unique when not null
ALTER TABLE virtual_accounts
ADD CONSTRAINT unique_brokerage_account_id
EXCLUDE (brokerage_account_id WITH =)
WHERE (brokerage_account_id IS NOT NULL);

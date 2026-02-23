-- Create bank_accounts table for storing user's linked bank accounts

CREATE TABLE IF NOT EXISTS bank_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bank_name VARCHAR(255) NOT NULL,
    account_number_last4 VARCHAR(4) NOT NULL,
    routing_number_last4 VARCHAR(4),
    iban VARCHAR(50),
    bic VARCHAR(11),
    currency VARCHAR(3) NOT NULL CHECK (currency IN ('USD', 'EUR')),
    is_verified BOOLEAN DEFAULT false,
    is_primary BOOLEAN DEFAULT false,
    bridge_recipient_id VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for faster queries
CREATE INDEX IF NOT EXISTS idx_bank_accounts_user_id ON bank_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_bank_accounts_user_id_primary ON bank_accounts(user_id, is_primary) WHERE is_primary = true;
CREATE INDEX IF NOT EXISTS idx_bank_accounts_bridge_recipient_id ON bank_accounts(bridge_recipient_id) WHERE bridge_recipient_id IS NOT NULL;

-- Add FK after bank_accounts exists (column added in migration 088)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'withdrawals'
          AND column_name = 'bank_account_id'
    )
    AND NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'fk_withdrawals_bank_account'
    ) THEN
        ALTER TABLE withdrawals
            ADD CONSTRAINT fk_withdrawals_bank_account
            FOREIGN KEY (bank_account_id)
            REFERENCES bank_accounts(id)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;

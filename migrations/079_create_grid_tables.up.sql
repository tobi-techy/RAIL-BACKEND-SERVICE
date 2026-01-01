-- Grid accounts table
CREATE TABLE IF NOT EXISTS grid_accounts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    address VARCHAR(64), -- Solana address, null until OTP verified
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    kyc_status VARCHAR(20) NOT NULL DEFAULT 'none',
    encrypted_session_secret TEXT, -- AES-256-GCM encrypted session secrets
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT grid_accounts_user_id_unique UNIQUE (user_id),
    CONSTRAINT grid_accounts_email_unique UNIQUE (email),
    CONSTRAINT grid_accounts_address_unique UNIQUE (address)
);

-- Grid virtual accounts table (for fiat on-ramp)
CREATE TABLE IF NOT EXISTS grid_virtual_accounts (
    id UUID PRIMARY KEY,
    grid_account_id UUID NOT NULL REFERENCES grid_accounts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_id VARCHAR(64) NOT NULL, -- Grid's virtual account ID
    account_number VARCHAR(32) NOT NULL,
    routing_number VARCHAR(16) NOT NULL,
    bank_name VARCHAR(128) NOT NULL,
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT grid_virtual_accounts_external_id_unique UNIQUE (external_id)
);

-- Grid payment intents table (for off-ramp)
CREATE TABLE IF NOT EXISTS grid_payment_intents (
    id UUID PRIMARY KEY,
    grid_account_id UUID NOT NULL REFERENCES grid_accounts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_id VARCHAR(64) NOT NULL, -- Grid's payment intent ID
    amount VARCHAR(32) NOT NULL, -- Stored as string for precision
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT grid_payment_intents_external_id_unique UNIQUE (external_id)
);

-- Indexes for efficient lookups
CREATE INDEX IF NOT EXISTS idx_grid_accounts_user_id ON grid_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_grid_accounts_email ON grid_accounts(email);
CREATE INDEX IF NOT EXISTS idx_grid_accounts_address ON grid_accounts(address);
CREATE INDEX IF NOT EXISTS idx_grid_virtual_accounts_user_id ON grid_virtual_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_grid_virtual_accounts_grid_account_id ON grid_virtual_accounts(grid_account_id);
CREATE INDEX IF NOT EXISTS idx_grid_payment_intents_user_id ON grid_payment_intents(user_id);
CREATE INDEX IF NOT EXISTS idx_grid_payment_intents_grid_account_id ON grid_payment_intents(grid_account_id);
CREATE INDEX IF NOT EXISTS idx_grid_payment_intents_status ON grid_payment_intents(status);

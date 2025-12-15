-- Onboarding & Managed Wallet Creation Migration
-- This migration adds support for KYC onboarding flow and Circle wallet integration

-- Update users table to support onboarding flow
-- Note: auth_provider_id, phone*, and onboarding_status already exist in users table
-- Adding only the missing columns
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS auth_provider_id VARCHAR(200), -- Auth provider reference (Cognito/Auth0)
    ADD COLUMN IF NOT EXISTS phone VARCHAR(20),
    ADD COLUMN IF NOT EXISTS phone_verified BOOLEAN DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS kyc_provider_ref VARCHAR(200), -- KYC provider reference
    ADD COLUMN IF NOT EXISTS kyc_submitted_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS kyc_approved_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS kyc_rejection_reason TEXT;

-- Note: Indexes for users table already exist from migration 001

-- Note: wallet_sets table already exists from migration 001

-- Update wallets table for Circle integration
-- Drop the old wallet table from migration 002 and recreate with proper Circle support
DROP TABLE IF EXISTS wallets CASCADE;

CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain VARCHAR(20) NOT NULL CHECK (chain IN ('ETH', 'ETH-SEPOLIA', 'SOL', 'SOL-DEVNET', 'APTOS', 'APTOS-TESTNET')),
    address VARCHAR(100) NOT NULL,
    circle_wallet_id VARCHAR(200) NOT NULL UNIQUE, -- Circle's wallet ID
    wallet_set_id UUID NOT NULL REFERENCES wallet_sets(id),
    account_type VARCHAR(10) NOT NULL DEFAULT 'EOA' CHECK (account_type IN ('EOA', 'SCA')),
    status VARCHAR(20) NOT NULL DEFAULT 'creating' CHECK (status IN ('creating', 'live', 'failed')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, chain)
);

-- Create indexes for wallets
CREATE INDEX idx_wallets_user_id ON wallets(user_id);
CREATE INDEX idx_wallets_chain ON wallets(chain);
CREATE INDEX idx_wallets_address ON wallets(address);
CREATE INDEX idx_wallets_circle_wallet_id ON wallets(circle_wallet_id);
CREATE INDEX idx_wallets_status ON wallets(status);
CREATE INDEX idx_wallets_wallet_set_id ON wallets(wallet_set_id);

-- Note: onboarding_flows, kyc_submissions, and wallet_provisioning_jobs tables already exist from migration 001
-- They were created with matching schema, so no need to recreate them

-- Update audit_logs table to support onboarding and wallet events
-- Add specific action types for onboarding flow
INSERT INTO audit_logs (id, action, resource_type, entity, after, at) VALUES 
(uuid_generate_v4(), 'MIGRATION', 'schema', 'onboarding_schema', '{"version": "003", "description": "Added onboarding and wallet management tables"}'::jsonb, NOW());

-- Note: Triggers already exist from migration 001

-- Insert a default wallet set for the application (to be configured with actual Circle credentials)
-- Note: This should be updated with real Circle wallet set ID during deployment
INSERT INTO wallet_sets (id, name, circle_wallet_set_id, entity_secret_ciphertext, status)
VALUES (
    '00000000-0000-0000-0000-000000000001'::uuid,
    'default-wallet-set',
    'placeholder-wallet-set-id',
    'placeholder-entity-secret',
    'active'
);

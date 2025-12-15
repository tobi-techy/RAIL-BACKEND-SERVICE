-- Rollback Onboarding & Managed Wallet Creation Migration

-- Drop triggers
DROP TRIGGER IF EXISTS update_wallet_provisioning_jobs_updated_at ON wallet_provisioning_jobs;
DROP TRIGGER IF EXISTS update_kyc_submissions_updated_at ON kyc_submissions;
DROP TRIGGER IF EXISTS update_onboarding_flows_updated_at ON onboarding_flows;
DROP TRIGGER IF EXISTS update_wallets_updated_at ON wallets;
DROP TRIGGER IF EXISTS update_wallet_sets_updated_at ON wallet_sets;

-- Drop tables in reverse order
DROP TABLE IF EXISTS wallet_provisioning_jobs CASCADE;
DROP TABLE IF EXISTS kyc_submissions CASCADE;
DROP TABLE IF EXISTS onboarding_flows CASCADE;
DROP TABLE IF EXISTS wallets CASCADE;
DROP TABLE IF EXISTS wallet_sets CASCADE;

-- Revert users table changes
ALTER TABLE users
    DROP COLUMN IF EXISTS kyc_rejection_reason,
    DROP COLUMN IF EXISTS kyc_approved_at,
    DROP COLUMN IF EXISTS kyc_submitted_at,
    DROP COLUMN IF EXISTS kyc_provider_ref,
    DROP COLUMN IF EXISTS onboarding_status,
    DROP COLUMN IF EXISTS phone_verified,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS auth_provider_id,
    -- Restore NOT NULL constraints
    ALTER COLUMN password_hash SET NOT NULL,
    ALTER COLUMN last_name SET NOT NULL,
    ALTER COLUMN first_name SET NOT NULL,
    ALTER COLUMN username SET NOT NULL;

-- Drop indexes created for users
DROP INDEX IF EXISTS idx_users_phone;
DROP INDEX IF EXISTS idx_users_onboarding_status;
DROP INDEX IF EXISTS idx_users_auth_provider_id;

-- Recreate the original wallets table from migration 002
CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain VARCHAR(20) NOT NULL CHECK (chain IN ('Aptos', 'Solana', 'polygon', 'starknet')),
    address VARCHAR(100) NOT NULL UNIQUE,
    provider_ref VARCHAR(200) NOT NULL, -- Reference to wallet manager (Circle, etc.)
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, chain)
);

-- Recreate indexes for original wallets table
CREATE INDEX idx_wallets_user_id ON wallets(user_id);
CREATE INDEX idx_wallets_chain ON wallets(chain);
CREATE INDEX idx_wallets_address ON wallets(address);
CREATE INDEX idx_wallets_provider_ref ON wallets(provider_ref);

-- Recreate trigger for wallets
CREATE TRIGGER update_wallets_updated_at BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Remove audit log entry
DELETE FROM audit_logs WHERE action = 'MIGRATION' AND entity = 'onboarding_schema';
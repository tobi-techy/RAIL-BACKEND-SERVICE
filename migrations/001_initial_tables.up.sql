-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- User Profile table with personal information
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    auth_provider_id TEXT,
    email TEXT NOT NULL UNIQUE,
    first_name TEXT,
    last_name TEXT,
    date_of_birth TIMESTAMP WITH TIME ZONE,
    phone TEXT,
    phone_verified BOOLEAN DEFAULT FALSE,
    email_verified BOOLEAN DEFAULT FALSE,
    onboarding_status TEXT NOT NULL DEFAULT 'started',
    kyc_status TEXT DEFAULT 'pending',
    kyc_provider_ref TEXT,
    kyc_submitted_at TIMESTAMP WITH TIME ZONE,
    kyc_approved_at TIMESTAMP WITH TIME ZONE,
    kyc_rejection_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Onboarding Flow table to track step-by-step progress
CREATE TABLE onboarding_flows (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    step TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    data JSONB DEFAULT '{}',
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(user_id, step)
);

-- KYC Submissions table to track KYC verification attempts
CREATE TABLE kyc_submissions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_ref TEXT NOT NULL UNIQUE,
    submission_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    verification_data JSONB DEFAULT '{}',
    rejection_reasons TEXT[] DEFAULT ARRAY[]::TEXT[],
    submitted_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    reviewed_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Wallet Sets table for Circle wallet set management
CREATE TABLE wallet_sets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    circle_wallet_set_id TEXT NOT NULL UNIQUE,
    entity_secret_ciphertext TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Managed Wallets table for user crypto wallets
CREATE TABLE managed_wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain TEXT NOT NULL,
    address TEXT NOT NULL,
    circle_wallet_id TEXT NOT NULL UNIQUE,
    wallet_set_id UUID NOT NULL REFERENCES wallet_sets(id) ON DELETE RESTRICT,
    account_type TEXT NOT NULL DEFAULT 'EOA',
    status TEXT NOT NULL DEFAULT 'creating',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(user_id, chain)
);

-- Wallet Provisioning Jobs table for async wallet creation
CREATE TABLE wallet_provisioning_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chains TEXT[] NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    attempt_count INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    circle_requests JSONB DEFAULT '{}',
    error_message TEXT,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_onboarding_status ON users(onboarding_status);
CREATE INDEX idx_users_kyc_status ON users(kyc_status);
CREATE INDEX idx_onboarding_flows_user_id ON onboarding_flows(user_id);
CREATE INDEX idx_onboarding_flows_status ON onboarding_flows(status);
CREATE INDEX idx_kyc_submissions_user_id ON kyc_submissions(user_id);
CREATE INDEX idx_kyc_submissions_status ON kyc_submissions(status);
CREATE INDEX idx_kyc_submissions_provider_ref ON kyc_submissions(provider_ref);
CREATE INDEX idx_managed_wallets_user_id ON managed_wallets(user_id);
CREATE INDEX idx_managed_wallets_chain ON managed_wallets(chain);
CREATE INDEX idx_managed_wallets_status ON managed_wallets(status);
CREATE INDEX idx_wallet_provisioning_jobs_user_id ON wallet_provisioning_jobs(user_id);
CREATE INDEX idx_wallet_provisioning_jobs_status ON wallet_provisioning_jobs(status);
CREATE INDEX idx_wallet_provisioning_jobs_next_retry ON wallet_provisioning_jobs(next_retry_at) WHERE next_retry_at IS NOT NULL;

-- Add constraints for enum-like fields
ALTER TABLE users ADD CONSTRAINT chk_onboarding_status 
    CHECK (onboarding_status IN ('started', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

ALTER TABLE users ADD CONSTRAINT chk_kyc_status 
    CHECK (kyc_status IN ('pending', 'processing', 'approved', 'rejected', 'expired'));

ALTER TABLE onboarding_flows ADD CONSTRAINT chk_step 
    CHECK (step IN ('registration', 'email_verification', 'phone_verification', 'kyc_submission', 'kyc_review', 'wallet_creation', 'completed'));

ALTER TABLE onboarding_flows ADD CONSTRAINT chk_status 
    CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'skipped'));

ALTER TABLE kyc_submissions ADD CONSTRAINT chk_kyc_submission_status 
    CHECK (status IN ('pending', 'processing', 'approved', 'rejected', 'expired'));

ALTER TABLE wallet_sets ADD CONSTRAINT chk_wallet_set_status 
    CHECK (status IN ('active', 'inactive'));

ALTER TABLE managed_wallets ADD CONSTRAINT chk_wallet_chain 
    CHECK (chain IN ('ETH', 'ETH-SEPOLIA', 'MATIC', 'AVAX', 'SOL', 'SOL-DEVNET', 'APTOS', 'APTOS-TESTNET'));

ALTER TABLE managed_wallets ADD CONSTRAINT chk_wallet_account_type 
    CHECK (account_type IN ('EOA', 'SCA'));

ALTER TABLE managed_wallets ADD CONSTRAINT chk_wallet_status 
    CHECK (status IN ('creating', 'live', 'failed'));

ALTER TABLE wallet_provisioning_jobs ADD CONSTRAINT chk_provisioning_status 
    CHECK (status IN ('queued', 'in_progress', 'completed', 'failed', 'retry'));

-- Update triggers for updated_at columns
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Sessions table
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    refresh_token_hash TEXT NOT NULL UNIQUE,
    ip_address INET,
    user_agent TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMP WITH TIME ZONE
);

-- Create indexes for sessions
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX idx_sessions_is_active ON sessions(is_active);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

-- Audit logs table
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(50) NOT NULL,
    resource_id VARCHAR(100),
    ip_address INET,
    user_agent TEXT,
    changes JSONB,
    status VARCHAR(20) NOT NULL DEFAULT 'success' CHECK (status IN ('success', 'failed')),
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes for audit logs
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_resource_type ON audit_logs(resource_type);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);

CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_onboarding_flows_updated_at BEFORE UPDATE ON onboarding_flows FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_kyc_submissions_updated_at BEFORE UPDATE ON kyc_submissions FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_wallet_sets_updated_at BEFORE UPDATE ON wallet_sets FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_managed_wallets_updated_at BEFORE UPDATE ON managed_wallets FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();
CREATE TRIGGER update_wallet_provisioning_jobs_updated_at BEFORE UPDATE ON wallet_provisioning_jobs FOR EACH ROW EXECUTE PROCEDURE update_updated_at_column();

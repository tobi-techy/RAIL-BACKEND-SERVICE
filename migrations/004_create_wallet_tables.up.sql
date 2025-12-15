-- Create wallet_sets table for managing Circle wallet sets
CREATE TABLE IF NOT EXISTS wallet_sets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    circle_wallet_set_id VARCHAR(255) UNIQUE NOT NULL,
    status VARCHAR(50) DEFAULT 'active' CHECK (
        status IN ('active', 'inactive', 'deprecated')
    ),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create managed_wallets table for tracking developer-controlled user wallets
CREATE TABLE IF NOT EXISTS managed_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_set_id UUID NOT NULL REFERENCES wallet_sets(id) ON DELETE RESTRICT,
    circle_wallet_id VARCHAR(255) UNIQUE NOT NULL, -- Circle wallet ID for transaction operations
    chain VARCHAR(50) NOT NULL CHECK (
        chain IN ('ETH', 'ETH-SEPOLIA', 'MATIC', 'MATIC-AMOY', 'SOL', 'SOL-DEVNET', 'APTOS', 'APTOS-TESTNET', 'AVAX', 'BASE', 'BASE-SEPOLIA')
    ),
    address VARCHAR(255) NOT NULL,
    account_type VARCHAR(10) DEFAULT 'EOA' CHECK (
        account_type IN ('EOA', 'SCA')
    ),
    status VARCHAR(50) DEFAULT 'creating' CHECK (
        status IN ('creating', 'live', 'failed')
    ),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, chain)
);

-- Create wallet_provisioning_jobs table for tracking wallet creation jobs
CREATE TABLE IF NOT EXISTS wallet_provisioning_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chains JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(50) DEFAULT 'queued' CHECK (
        status IN ('queued', 'in_progress', 'completed', 'failed', 'retry')
    ),
    attempt_count INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    error_message TEXT,
    next_retry_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_wallet_sets_status ON wallet_sets(status);
CREATE INDEX IF NOT EXISTS idx_wallet_sets_circle_id ON wallet_sets(circle_wallet_set_id);

CREATE INDEX IF NOT EXISTS idx_managed_wallets_user_id ON managed_wallets(user_id);
CREATE INDEX IF NOT EXISTS idx_managed_wallets_wallet_set_id ON managed_wallets(wallet_set_id);
CREATE INDEX IF NOT EXISTS idx_managed_wallets_chain ON managed_wallets(chain);
CREATE INDEX IF NOT EXISTS idx_managed_wallets_status ON managed_wallets(status);
CREATE INDEX IF NOT EXISTS idx_managed_wallets_circle_id ON managed_wallets(circle_wallet_id);

CREATE INDEX IF NOT EXISTS idx_wallet_provisioning_jobs_user_id ON wallet_provisioning_jobs(user_id);
CREATE INDEX IF NOT EXISTS idx_wallet_provisioning_jobs_status ON wallet_provisioning_jobs(status);
CREATE INDEX IF NOT EXISTS idx_wallet_provisioning_jobs_retry ON wallet_provisioning_jobs(next_retry_at) WHERE status = 'failed';

-- Add comments to tables
COMMENT ON TABLE wallet_sets IS 'Circle wallet sets for organizing user wallets';
COMMENT ON TABLE managed_wallets IS 'Individual user wallets managed by Circle';
COMMENT ON TABLE wallet_provisioning_jobs IS 'Background jobs for creating user wallets';
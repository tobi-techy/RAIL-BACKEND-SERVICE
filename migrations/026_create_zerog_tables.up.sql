-- 0G Storage Backups Table
CREATE TABLE IF NOT EXISTS zerog_storage_backups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    namespace VARCHAR(255) NOT NULL,
    storage_id VARCHAR(255) NOT NULL UNIQUE,
    checksum VARCHAR(64) NOT NULL,
    size BIGINT NOT NULL,
    backed_up_at TIMESTAMP NOT NULL DEFAULT NOW(),
    verified_at TIMESTAMP,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_zerog_backups_user_id ON zerog_storage_backups(user_id);
CREATE INDEX idx_zerog_backups_storage_id ON zerog_storage_backups(storage_id);
CREATE INDEX idx_zerog_backups_status ON zerog_storage_backups(status);
CREATE INDEX idx_zerog_backups_verified_at ON zerog_storage_backups(verified_at);

-- 0G User Quotas Table
CREATE TABLE IF NOT EXISTS zerog_user_quotas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    tier VARCHAR(50) NOT NULL DEFAULT 'free',
    storage_bytes BIGINT NOT NULL DEFAULT 0,
    storage_limit BIGINT NOT NULL DEFAULT 1073741824,
    compute_tokens BIGINT NOT NULL DEFAULT 0,
    compute_limit BIGINT NOT NULL DEFAULT 100000,
    monthly_cost DECIMAL(10, 2) NOT NULL DEFAULT 0.00,
    monthly_cost_limit DECIMAL(10, 2) NOT NULL DEFAULT 10.00,
    reset_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_zerog_quotas_user_id ON zerog_user_quotas(user_id);
CREATE INDEX idx_zerog_quotas_reset_at ON zerog_user_quotas(reset_at);

-- 0G Cost Tracking Table
CREATE TABLE IF NOT EXISTS zerog_cost_tracking (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    service VARCHAR(50) NOT NULL,
    operation VARCHAR(50) NOT NULL,
    amount DECIMAL(10, 4) NOT NULL,
    metadata JSONB,
    recorded_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_zerog_costs_user_id ON zerog_cost_tracking(user_id);
CREATE INDEX idx_zerog_costs_service ON zerog_cost_tracking(service);
CREATE INDEX idx_zerog_costs_recorded_at ON zerog_cost_tracking(recorded_at);

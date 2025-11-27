-- 058_create_treasury_tables.up.sql
-- Treasury Engine: Conversion jobs, buffer thresholds, and provider management

-- ============================================================================
-- ENUM TYPES
-- ============================================================================

-- Conversion direction: USDC->USD (off-ramp) or USD->USDC (on-ramp)
CREATE TYPE conversion_direction AS ENUM (
    'usdc_to_usd',  -- Off-ramp: Convert USDC to USD
    'usd_to_usdc'   -- On-ramp: Convert USD to USDC
);

-- Conversion job status tracking
CREATE TYPE conversion_job_status AS ENUM (
    'pending',           -- Job created, waiting to execute
    'provider_submitted', -- Submitted to conversion provider
    'provider_processing', -- Provider is processing conversion
    'provider_completed',  -- Provider completed conversion
    'ledger_updating',    -- Updating ledger entries
    'completed',         -- Fully completed with ledger updated
    'failed',            -- Conversion failed
    'cancelled'          -- Job was cancelled
);

-- Conversion trigger reason for auditing
CREATE TYPE conversion_trigger AS ENUM (
    'buffer_replenishment',  -- Buffer fell below threshold
    'scheduled_rebalance',   -- Scheduled rebalancing
    'manual',                -- Manually triggered by operator
    'emergency'              -- Emergency top-up
);

-- Provider status
CREATE TYPE provider_status AS ENUM (
    'active',
    'inactive',
    'degraded'
);

-- ============================================================================
-- TABLE: conversion_providers
-- ============================================================================
-- Stores configuration for conversion providers (Due, ZeroHash, etc.)

CREATE TABLE conversion_providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    provider_type VARCHAR(50) NOT NULL,  -- 'due', 'zerohash', etc.
    priority INTEGER NOT NULL,  -- Lower number = higher priority
    status provider_status NOT NULL DEFAULT 'active',
    
    -- Configuration
    supports_usdc_to_usd BOOLEAN NOT NULL DEFAULT true,
    supports_usd_to_usdc BOOLEAN NOT NULL DEFAULT true,
    min_conversion_amount NUMERIC(20, 6) NOT NULL DEFAULT 100.00,
    max_conversion_amount NUMERIC(20, 6),
    
    -- Rate limits
    daily_volume_limit NUMERIC(20, 6),
    daily_volume_used NUMERIC(20, 6) NOT NULL DEFAULT 0,
    
    -- Health tracking
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_success_at TIMESTAMP,
    last_failure_at TIMESTAMP,
    
    -- Metadata
    api_credentials_encrypted TEXT,  -- Encrypted API credentials
    webhook_secret TEXT,
    notes TEXT,
    
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_conversion_providers_priority ON conversion_providers(priority) WHERE status = 'active';
CREATE INDEX idx_conversion_providers_status ON conversion_providers(status);

-- ============================================================================
-- TABLE: buffer_thresholds
-- ============================================================================
-- Configuration for buffer management thresholds per account type

CREATE TABLE buffer_thresholds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_type VARCHAR(50) NOT NULL UNIQUE,  -- References ledger account_type
    
    -- Threshold configuration (in USD equivalent)
    min_threshold NUMERIC(20, 6) NOT NULL,      -- Alert and trigger conversion if below
    target_threshold NUMERIC(20, 6) NOT NULL,   -- Replenish to this level
    max_threshold NUMERIC(20, 6) NOT NULL,      -- Alert if exceeds (over-capitalized)
    
    -- Conversion strategy
    conversion_batch_size NUMERIC(20, 6) NOT NULL DEFAULT 10000.00,  -- Default batch size
    
    -- Metadata
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    CONSTRAINT chk_threshold_ordering CHECK (min_threshold < target_threshold AND target_threshold < max_threshold),
    CONSTRAINT chk_positive_thresholds CHECK (min_threshold > 0 AND target_threshold > 0 AND max_threshold > 0)
);

-- Indexes
CREATE INDEX idx_buffer_thresholds_account_type ON buffer_thresholds(account_type);

-- ============================================================================
-- TABLE: conversion_jobs
-- ============================================================================
-- Tracks all conversion operations (USDC<->USD)

CREATE TABLE conversion_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Conversion details
    direction conversion_direction NOT NULL,
    amount NUMERIC(20, 6) NOT NULL,  -- Amount to convert (in source currency)
    status conversion_job_status NOT NULL DEFAULT 'pending',
    trigger_reason conversion_trigger NOT NULL,
    
    -- Provider tracking
    provider_id UUID REFERENCES conversion_providers(id),
    provider_name VARCHAR(100),  -- Denormalized for historical tracking
    provider_tx_id VARCHAR(255),  -- External provider transaction ID
    provider_response JSONB,  -- Full provider response
    
    -- Ledger integration
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    
    -- Source and destination accounts
    source_account_id UUID REFERENCES ledger_accounts(id),
    destination_account_id UUID REFERENCES ledger_accounts(id),
    
    -- Conversion results
    source_amount NUMERIC(20, 6),  -- Actual amount debited
    destination_amount NUMERIC(20, 6),  -- Actual amount credited
    exchange_rate NUMERIC(20, 10),  -- Applied exchange rate
    fees_paid NUMERIC(20, 6),  -- Provider fees
    
    -- Timing
    scheduled_at TIMESTAMP,  -- When job was scheduled
    submitted_at TIMESTAMP,  -- When submitted to provider
    provider_completed_at TIMESTAMP,  -- When provider completed
    completed_at TIMESTAMP,  -- When fully completed (ledger updated)
    failed_at TIMESTAMP,
    
    -- Error tracking
    error_message TEXT,
    error_code VARCHAR(100),
    retry_count INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    
    -- Idempotency
    idempotency_key VARCHAR(255) UNIQUE,
    
    -- Metadata
    notes TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    CONSTRAINT chk_positive_amount CHECK (amount > 0)
);

-- Indexes for performance
CREATE INDEX idx_conversion_jobs_status ON conversion_jobs(status);
CREATE INDEX idx_conversion_jobs_direction ON conversion_jobs(direction);
CREATE INDEX idx_conversion_jobs_provider_id ON conversion_jobs(provider_id);
CREATE INDEX idx_conversion_jobs_provider_tx_id ON conversion_jobs(provider_tx_id) WHERE provider_tx_id IS NOT NULL;
CREATE INDEX idx_conversion_jobs_ledger_tx ON conversion_jobs(ledger_transaction_id) WHERE ledger_transaction_id IS NOT NULL;
CREATE INDEX idx_conversion_jobs_created_at ON conversion_jobs(created_at DESC);
CREATE INDEX idx_conversion_jobs_scheduled_at ON conversion_jobs(scheduled_at) WHERE status = 'pending';
CREATE INDEX idx_conversion_jobs_idempotency ON conversion_jobs(idempotency_key) WHERE idempotency_key IS NOT NULL;

-- ============================================================================
-- TABLE: conversion_job_history
-- ============================================================================
-- Audit trail for all status changes in conversion jobs

CREATE TABLE conversion_job_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversion_job_id UUID NOT NULL REFERENCES conversion_jobs(id) ON DELETE CASCADE,
    
    previous_status conversion_job_status,
    new_status conversion_job_status NOT NULL,
    
    notes TEXT,
    metadata JSONB,
    
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_conversion_job_history_job_id ON conversion_job_history(conversion_job_id, created_at DESC);

-- ============================================================================
-- TRIGGER: Update timestamp on row update
-- ============================================================================

CREATE OR REPLACE FUNCTION update_treasury_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_conversion_providers_updated_at
    BEFORE UPDATE ON conversion_providers
    FOR EACH ROW
    EXECUTE FUNCTION update_treasury_updated_at();

CREATE TRIGGER trg_buffer_thresholds_updated_at
    BEFORE UPDATE ON buffer_thresholds
    FOR EACH ROW
    EXECUTE FUNCTION update_treasury_updated_at();

CREATE TRIGGER trg_conversion_jobs_updated_at
    BEFORE UPDATE ON conversion_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_treasury_updated_at();

-- ============================================================================
-- TRIGGER: Audit conversion job status changes
-- ============================================================================

CREATE OR REPLACE FUNCTION audit_conversion_job_status_change()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO conversion_job_history (
            conversion_job_id,
            previous_status,
            new_status,
            notes,
            metadata
        ) VALUES (
            NEW.id,
            OLD.status,
            NEW.status,
            CASE
                WHEN NEW.status = 'failed' THEN NEW.error_message
                WHEN NEW.status = 'completed' THEN 'Conversion completed successfully'
                ELSE NULL
            END,
            jsonb_build_object(
                'retry_count', NEW.retry_count,
                'provider_tx_id', NEW.provider_tx_id,
                'updated_by', current_user
            )
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_conversion_job_status_audit
    AFTER UPDATE ON conversion_jobs
    FOR EACH ROW
    EXECUTE FUNCTION audit_conversion_job_status_change();

-- ============================================================================
-- SEED DATA: Default conversion providers
-- ============================================================================

INSERT INTO conversion_providers (name, provider_type, priority, status, supports_usdc_to_usd, supports_usd_to_usdc, min_conversion_amount, max_conversion_amount)
VALUES 
    ('Due', 'due', 1, 'active', true, true, 100.00, 500000.00),
    ('ZeroHash', 'zerohash', 2, 'inactive', true, true, 100.00, 1000000.00);

-- ============================================================================
-- SEED DATA: Default buffer thresholds
-- ============================================================================

INSERT INTO buffer_thresholds (account_type, min_threshold, target_threshold, max_threshold, conversion_batch_size, notes)
VALUES 
    ('system_buffer_usdc', 10000.00, 50000.00, 100000.00, 20000.00, 'On-chain USDC buffer for instant withdrawals'),
    ('system_buffer_fiat', 20000.00, 100000.00, 200000.00, 50000.00, 'Operational USD at conversion provider'),
    ('broker_operational', 50000.00, 200000.00, 500000.00, 100000.00, 'Pre-funded cash at Alpaca broker');

-- ============================================================================
-- HELPER VIEWS
-- ============================================================================

-- View: Active conversion jobs with provider details
CREATE VIEW v_active_conversion_jobs AS
SELECT 
    cj.id,
    cj.direction,
    cj.amount,
    cj.status,
    cj.trigger_reason,
    cp.name as provider_name,
    cp.provider_type,
    cj.provider_tx_id,
    cj.scheduled_at,
    cj.created_at,
    cj.retry_count,
    cj.error_message
FROM conversion_jobs cj
LEFT JOIN conversion_providers cp ON cj.provider_id = cp.id
WHERE cj.status NOT IN ('completed', 'failed', 'cancelled')
ORDER BY cj.scheduled_at ASC NULLS FIRST;

-- View: Provider health metrics
CREATE VIEW v_provider_health AS
SELECT 
    id,
    name,
    provider_type,
    status,
    priority,
    success_count,
    failure_count,
    CASE 
        WHEN (success_count + failure_count) > 0 
        THEN ROUND((success_count::numeric / (success_count + failure_count)::numeric) * 100, 2)
        ELSE NULL
    END as success_rate_pct,
    daily_volume_used,
    daily_volume_limit,
    last_success_at,
    last_failure_at
FROM conversion_providers
ORDER BY priority;

-- View: Buffer status with thresholds
CREATE VIEW v_buffer_status AS
SELECT 
    bt.account_type,
    bt.min_threshold,
    bt.target_threshold,
    bt.max_threshold,
    la.balance as current_balance,
    CASE
        WHEN la.balance < bt.min_threshold THEN 'CRITICAL_LOW'
        WHEN la.balance < bt.target_threshold THEN 'BELOW_TARGET'
        WHEN la.balance > bt.max_threshold THEN 'OVER_CAPITALIZED'
        ELSE 'HEALTHY'
    END as status,
    (bt.target_threshold - la.balance) as amount_to_target
FROM buffer_thresholds bt
JOIN ledger_accounts la ON la.account_type = bt.account_type
ORDER BY 
    CASE
        WHEN la.balance < bt.min_threshold THEN 1
        WHEN la.balance < bt.target_threshold THEN 2
        WHEN la.balance > bt.max_threshold THEN 3
        ELSE 4
    END;

COMMENT ON VIEW v_buffer_status IS 'Real-time buffer health status with alerts';

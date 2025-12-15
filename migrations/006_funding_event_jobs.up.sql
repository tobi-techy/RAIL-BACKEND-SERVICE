-- Funding Event Jobs Table for Webhook Reliability Worker
-- Supports retry logic, DLQ, and reconciliation

CREATE TABLE funding_event_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tx_hash VARCHAR(100) NOT NULL,
    chain VARCHAR(20) NOT NULL CHECK (chain IN ('Aptos', 'Solana', 'polygon', 'starknet')),
    token VARCHAR(20) NOT NULL CHECK (token IN ('USDC')),
    amount DECIMAL(36, 18) NOT NULL CHECK (amount > 0),
    to_address VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'dlq')),
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    
    -- Error tracking
    last_error TEXT,
    error_type VARCHAR(20) CHECK (error_type IN ('transient', 'permanent', 'unknown', 'rpc_failure')),
    failure_reason TEXT,
    
    -- Timing
    first_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_attempt_at TIMESTAMP WITH TIME ZONE,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    moved_to_dlq_at TIMESTAMP WITH TIME ZONE,
    
    -- Metadata
    webhook_payload JSONB,
    processing_logs JSONB DEFAULT '[]'::jsonb,
    
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Unique constraint for idempotency (tx_hash + chain combination)
    UNIQUE(tx_hash, chain)
);

-- Create indexes for efficient querying
CREATE INDEX idx_funding_event_jobs_status ON funding_event_jobs(status);
CREATE INDEX idx_funding_event_jobs_tx_hash ON funding_event_jobs(tx_hash);
CREATE INDEX idx_funding_event_jobs_next_retry ON funding_event_jobs(next_retry_at) WHERE next_retry_at IS NOT NULL;
CREATE INDEX idx_funding_event_jobs_pending ON funding_event_jobs(first_seen_at) WHERE status = 'pending';
CREATE INDEX idx_funding_event_jobs_dlq ON funding_event_jobs(moved_to_dlq_at) WHERE status = 'dlq';
CREATE INDEX idx_funding_event_jobs_chain ON funding_event_jobs(chain);
CREATE INDEX idx_funding_event_jobs_created_at ON funding_event_jobs(created_at DESC);

-- Update trigger for updated_at
CREATE TRIGGER update_funding_event_jobs_updated_at 
    BEFORE UPDATE ON funding_event_jobs 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Add comment for documentation
COMMENT ON TABLE funding_event_jobs IS 'Tracks webhook events for deposit processing with retry and DLQ support';
COMMENT ON COLUMN funding_event_jobs.status IS 'Current status: pending, processing, completed, failed, dlq';
COMMENT ON COLUMN funding_event_jobs.error_type IS 'Categorizes errors: transient (retry), permanent (no retry), rpc_failure, unknown';
COMMENT ON COLUMN funding_event_jobs.next_retry_at IS 'Scheduled time for next retry attempt with exponential backoff';
COMMENT ON COLUMN funding_event_jobs.processing_logs IS 'JSON array of processing attempts with timestamps and errors';

-- Persist incoming Sumsub webhook events for idempotency/audit
CREATE TABLE IF NOT EXISTS sumsub_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key TEXT NOT NULL,
    applicant_id TEXT NOT NULL,
    correlation_id TEXT,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    received_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_sumsub_webhook_events_applicant_id ON sumsub_webhook_events(applicant_id);
CREATE INDEX IF NOT EXISTS idx_sumsub_webhook_events_created_at ON sumsub_webhook_events(created_at DESC);

-- Queue asynchronous sync jobs to Bridge/Alpaca
CREATE TABLE IF NOT EXISTS kyc_sync_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key TEXT NOT NULL,
    applicant_id TEXT NOT NULL,
    correlation_id TEXT,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'dlq')),
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_kyc_sync_jobs_status ON kyc_sync_jobs(status);
CREATE INDEX IF NOT EXISTS idx_kyc_sync_jobs_next_retry_at ON kyc_sync_jobs(next_retry_at) WHERE next_retry_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_kyc_sync_jobs_pending ON kyc_sync_jobs(created_at) WHERE status IN ('pending', 'retry');
CREATE INDEX IF NOT EXISTS idx_kyc_sync_jobs_applicant_id ON kyc_sync_jobs(applicant_id);

DROP TRIGGER IF EXISTS update_kyc_sync_jobs_updated_at ON kyc_sync_jobs;
CREATE TRIGGER update_kyc_sync_jobs_updated_at
    BEFORE UPDATE ON kyc_sync_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

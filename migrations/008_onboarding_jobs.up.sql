-- Create onboarding jobs table for async processing
CREATE TABLE onboarding_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (
        status IN ('queued', 'in_progress', 'completed', 'failed', 'retry')
    ),
    job_type TEXT NOT NULL DEFAULT 'full_onboarding' CHECK (
        job_type IN ('full_onboarding', 'kyc_only', 'wallet_only')
    ),
    payload JSONB DEFAULT '{}',
    attempt_count INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 5,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for efficient querying
CREATE INDEX idx_onboarding_jobs_user_id ON onboarding_jobs(user_id);
CREATE INDEX idx_onboarding_jobs_status ON onboarding_jobs(status);
CREATE INDEX idx_onboarding_jobs_next_retry_at ON onboarding_jobs(next_retry_at);
CREATE INDEX idx_onboarding_jobs_created_at ON onboarding_jobs(created_at);

-- Create composite index for worker queries
CREATE INDEX idx_onboarding_jobs_status_retry ON onboarding_jobs(status, next_retry_at) 
WHERE status IN ('queued', 'retry');

-- Add comments
COMMENT ON TABLE onboarding_jobs IS 'Queue for async onboarding processing jobs';
COMMENT ON COLUMN onboarding_jobs.user_id IS 'User ID for the onboarding job';
COMMENT ON COLUMN onboarding_jobs.status IS 'Current status of the job';
COMMENT ON COLUMN onboarding_jobs.job_type IS 'Type of onboarding job to process';
COMMENT ON COLUMN onboarding_jobs.payload IS 'Job-specific data and configuration';
COMMENT ON COLUMN onboarding_jobs.attempt_count IS 'Number of processing attempts';
COMMENT ON COLUMN onboarding_jobs.max_attempts IS 'Maximum number of retry attempts';
COMMENT ON COLUMN onboarding_jobs.next_retry_at IS 'When to retry failed jobs';
COMMENT ON COLUMN onboarding_jobs.error_message IS 'Last error message if job failed';

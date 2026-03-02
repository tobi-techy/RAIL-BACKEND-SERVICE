ALTER TABLE kyc_sync_jobs
ADD COLUMN IF NOT EXISTS provider VARCHAR(20);

CREATE INDEX IF NOT EXISTS idx_kyc_sync_jobs_provider ON kyc_sync_jobs(provider) WHERE provider IS NOT NULL;

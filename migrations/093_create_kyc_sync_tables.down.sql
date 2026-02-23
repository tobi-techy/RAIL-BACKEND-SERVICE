DROP TRIGGER IF EXISTS update_kyc_sync_jobs_updated_at ON kyc_sync_jobs;
DROP INDEX IF EXISTS idx_kyc_sync_jobs_applicant_id;
DROP INDEX IF EXISTS idx_kyc_sync_jobs_pending;
DROP INDEX IF EXISTS idx_kyc_sync_jobs_next_retry_at;
DROP INDEX IF EXISTS idx_kyc_sync_jobs_status;
DROP TABLE IF EXISTS kyc_sync_jobs;

DROP INDEX IF EXISTS idx_sumsub_webhook_events_created_at;
DROP INDEX IF EXISTS idx_sumsub_webhook_events_applicant_id;
DROP TABLE IF EXISTS sumsub_webhook_events;

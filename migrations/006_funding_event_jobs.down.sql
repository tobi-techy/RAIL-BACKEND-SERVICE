-- Rollback funding event jobs table

DROP TRIGGER IF EXISTS update_funding_event_jobs_updated_at ON funding_event_jobs;
DROP INDEX IF EXISTS idx_funding_event_jobs_created_at;
DROP INDEX IF EXISTS idx_funding_event_jobs_chain;
DROP INDEX IF EXISTS idx_funding_event_jobs_dlq;
DROP INDEX IF EXISTS idx_funding_event_jobs_pending;
DROP INDEX IF EXISTS idx_funding_event_jobs_next_retry;
DROP INDEX IF EXISTS idx_funding_event_jobs_tx_hash;
DROP INDEX IF EXISTS idx_funding_event_jobs_status;
DROP TABLE IF EXISTS funding_event_jobs;

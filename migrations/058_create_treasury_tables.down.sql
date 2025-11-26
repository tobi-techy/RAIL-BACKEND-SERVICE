-- 058_create_treasury_tables.down.sql
-- Rollback script for treasury tables

-- Drop views
DROP VIEW IF EXISTS v_buffer_status;
DROP VIEW IF EXISTS v_provider_health;
DROP VIEW IF EXISTS v_active_conversion_jobs;

-- Drop triggers
DROP TRIGGER IF EXISTS trg_conversion_job_status_audit ON conversion_jobs;
DROP TRIGGER IF EXISTS trg_conversion_jobs_updated_at ON conversion_jobs;
DROP TRIGGER IF EXISTS trg_buffer_thresholds_updated_at ON buffer_thresholds;
DROP TRIGGER IF EXISTS trg_conversion_providers_updated_at ON conversion_providers;

-- Drop trigger functions
DROP FUNCTION IF EXISTS audit_conversion_job_status_change();
DROP FUNCTION IF EXISTS update_treasury_updated_at();

-- Drop tables (in reverse order of dependencies)
DROP TABLE IF EXISTS conversion_job_history;
DROP TABLE IF EXISTS conversion_jobs;
DROP TABLE IF EXISTS buffer_thresholds;
DROP TABLE IF EXISTS conversion_providers;

-- Drop enum types
DROP TYPE IF EXISTS provider_status;
DROP TYPE IF EXISTS conversion_trigger;
DROP TYPE IF EXISTS conversion_job_status;
DROP TYPE IF EXISTS conversion_direction;

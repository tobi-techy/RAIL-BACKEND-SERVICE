DROP INDEX IF EXISTS idx_performance_history_conductor;
DROP INDEX IF EXISTS idx_execution_logs_signal;
DROP INDEX IF EXISTS idx_execution_logs_draft;
DROP INDEX IF EXISTS idx_signals_created;
DROP INDEX IF EXISTS idx_signals_status;
DROP INDEX IF EXISTS idx_signals_conductor;
DROP INDEX IF EXISTS idx_drafts_status;
DROP INDEX IF EXISTS idx_drafts_conductor;
DROP INDEX IF EXISTS idx_drafts_drafter;
DROP INDEX IF EXISTS idx_conductors_return;
DROP INDEX IF EXISTS idx_conductors_followers;
DROP INDEX IF EXISTS idx_conductors_status;

DROP TABLE IF EXISTS conductor_performance_history;
DROP TABLE IF EXISTS signal_execution_logs;
DROP TABLE IF EXISTS signals;
DROP TABLE IF EXISTS drafts;
DROP TABLE IF EXISTS conductors;

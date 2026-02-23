-- Rollback Investment Rules Tables

DROP INDEX IF EXISTS idx_dividend_events_symbol;
DROP INDEX IF EXISTS idx_dividend_events_pending;
DROP INDEX IF EXISTS idx_dividend_events_user_id;
DROP TABLE IF EXISTS dividend_events;

DROP INDEX IF EXISTS idx_pending_withdrawals_ready;
DROP INDEX IF EXISTS idx_pending_withdrawals_status;
DROP INDEX IF EXISTS idx_pending_withdrawals_user_id;
DROP TABLE IF EXISTS pending_withdrawals;

DROP INDEX IF EXISTS idx_milestones_uncelebrated;
DROP INDEX IF EXISTS idx_milestones_user_id;
DROP TABLE IF EXISTS investment_milestones;

DROP INDEX IF EXISTS idx_investment_rules_rebalancing;
DROP INDEX IF EXISTS idx_investment_rules_user_id;
DROP TABLE IF EXISTS investment_rules_config;

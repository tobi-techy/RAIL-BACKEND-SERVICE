-- Rollback Portfolio Performance Tracking Migration

DROP INDEX IF EXISTS idx_portfolio_performance_user_date;
DROP INDEX IF EXISTS idx_portfolio_performance_date;
DROP INDEX IF EXISTS idx_portfolio_performance_user_id;
DROP TABLE IF EXISTS portfolio_performance;

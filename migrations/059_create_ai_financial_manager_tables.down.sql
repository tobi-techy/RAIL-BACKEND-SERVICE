-- Rollback AI Financial Manager Tables Migration

-- Drop trigger
DROP TRIGGER IF EXISTS update_investment_streaks_updated_at ON investment_streaks;

-- Drop new columns from ai_summaries
ALTER TABLE ai_summaries DROP COLUMN IF EXISTS summary_type;
ALTER TABLE ai_summaries DROP COLUMN IF EXISTS cards_json;
ALTER TABLE ai_summaries DROP COLUMN IF EXISTS insights_json;

-- Drop tables in reverse order (respecting foreign key dependencies)
DROP TABLE IF EXISTS rebalance_previews;
DROP TABLE IF EXISTS basket_recommendations;
DROP TABLE IF EXISTS ai_chat_messages;
DROP TABLE IF EXISTS ai_chat_sessions;
DROP TABLE IF EXISTS user_news;
DROP TABLE IF EXISTS investment_streaks;
DROP TABLE IF EXISTS user_contributions;

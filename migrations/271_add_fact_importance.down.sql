DROP INDEX IF EXISTS idx_miriam_user_facts_importance_recency;
ALTER TABLE miriam_user_facts DROP COLUMN IF EXISTS importance;

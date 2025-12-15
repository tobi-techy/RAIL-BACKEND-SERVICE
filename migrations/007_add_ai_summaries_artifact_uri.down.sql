-- Remove artifact_uri field from ai_summaries table
-- This down migration reverses the 0G storage integration changes

-- Drop the index first
DROP INDEX IF EXISTS idx_ai_summaries_artifact_uri;

-- Remove the artifact_uri column
ALTER TABLE ai_summaries 
DROP COLUMN IF EXISTS artifact_uri;
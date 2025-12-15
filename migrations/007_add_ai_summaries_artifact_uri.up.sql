-- Add artifact_uri field to ai_summaries table for 0G storage integration
-- This migration extends the AI summaries functionality to support 0G storage URIs

-- Add artifact_uri column to store 0G storage references
ALTER TABLE ai_summaries 
ADD COLUMN artifact_uri VARCHAR(500);

-- Add index for efficient artifact_uri lookups
CREATE INDEX idx_ai_summaries_artifact_uri ON ai_summaries(artifact_uri) WHERE artifact_uri IS NOT NULL;

-- Add comments for documentation
COMMENT ON COLUMN ai_summaries.artifact_uri IS '0G storage URI for AI-generated artifacts and detailed analysis data';
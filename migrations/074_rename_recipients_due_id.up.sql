-- Rename due_id to provider_id in recipients table
ALTER TABLE recipients RENAME COLUMN due_id TO provider_id;

-- Update index
DROP INDEX IF EXISTS idx_recipients_due_id;
CREATE INDEX IF NOT EXISTS idx_recipients_provider_id ON recipients(provider_id);

-- Revert provider_id back to due_id
ALTER TABLE recipients RENAME COLUMN provider_id TO due_id;

DROP INDEX IF EXISTS idx_recipients_provider_id;
CREATE INDEX IF NOT EXISTS idx_recipients_due_id ON recipients(due_id);

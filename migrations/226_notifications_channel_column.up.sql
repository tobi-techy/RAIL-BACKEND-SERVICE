-- Add channel column to notifications table.
-- The column exists in prod with NOT NULL but was never in the migration files.
-- Default 'in_app' matches the service's primary delivery path and keeps
-- existing INSERTs (which don't supply channel) working without code changes.
ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS channel VARCHAR(20) NOT NULL DEFAULT 'in_app'
        CHECK (channel IN ('in_app', 'push', 'email', 'sms'));

-- Add TOS acceptance timestamp to user_settings
ALTER TABLE user_settings ADD COLUMN IF NOT EXISTS tos_accepted_at TIMESTAMP WITH TIME ZONE;

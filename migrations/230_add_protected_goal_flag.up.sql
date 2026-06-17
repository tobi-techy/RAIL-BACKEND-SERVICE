-- Add 'protected' flag to shared_goals.
-- Protected goals cannot be raided by stash withdrawals.
ALTER TABLE shared_goals ADD COLUMN IF NOT EXISTS protected BOOLEAN NOT NULL DEFAULT false;

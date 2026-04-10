-- Add basic_complete to onboarding_status enum
ALTER TYPE onboarding_status ADD VALUE IF NOT EXISTS 'basic_complete' AFTER 'started';

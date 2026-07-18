-- Add control_level column to miriam_tone_profiles
ALTER TABLE miriam_tone_profiles ADD COLUMN IF NOT EXISTS control_level TEXT NOT NULL DEFAULT 'full';

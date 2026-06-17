ALTER TABLE miriam_tone_profiles ADD COLUMN IF NOT EXISTS personality_mode TEXT NOT NULL DEFAULT 'default';

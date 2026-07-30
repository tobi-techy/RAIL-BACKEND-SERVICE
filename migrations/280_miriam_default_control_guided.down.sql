-- Revert default autonomy to full (pre-MVP guided default).
ALTER TABLE miriam_tone_profiles
    ALTER COLUMN control_level SET DEFAULT 'full';

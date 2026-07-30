-- MVP: default autonomy is Guided (suggest + confirm), not Full Autopilot.
-- Silent money moves require an explicit switch to full + an active mandate.
ALTER TABLE miriam_tone_profiles
    ALTER COLUMN control_level SET DEFAULT 'guided';

-- Existing rows that never customized autonomy still show the old product default
-- of full. Leave intentionally-set rows alone; only rewrite the historical default.
UPDATE miriam_tone_profiles
SET control_level = 'guided',
    updated_at = NOW()
WHERE control_level = 'full'
  AND sample_count = 0;

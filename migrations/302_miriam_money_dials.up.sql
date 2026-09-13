-- Miriam money dials: the one or two things a person genuinely loves spending
-- on (food, travel, family, fashion), captured during their first conversations.
-- Miriam uses these to give permission to spend guilt-free on what they love and
-- cut mercilessly on what they don't — the Ramit conscious-spending read.

ALTER TABLE miriam_tone_profiles
    ADD COLUMN IF NOT EXISTS money_dials TEXT NOT NULL DEFAULT '';

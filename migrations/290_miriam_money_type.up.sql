-- Miriam money type: the read Miriam forms about a person's relationship with
-- money during their first conversations (avoider / optimizer / worrier /
-- dreamer). Drives tone before the EMA tone profile has enough samples.

ALTER TABLE miriam_tone_profiles
    ADD COLUMN IF NOT EXISTS money_type VARCHAR(20) NOT NULL DEFAULT '' CHECK (money_type IN (
        '', 'avoider', 'optimizer', 'worrier', 'dreamer'
    ));

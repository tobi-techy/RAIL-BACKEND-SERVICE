ALTER TABLE miriam_tone_profiles
    DROP CONSTRAINT IF EXISTS miriam_tone_profiles_locale_style_check;

ALTER TABLE miriam_tone_profiles
    DROP COLUMN IF EXISTS locale_style;

ALTER TABLE miriam_user_facts
    DROP CONSTRAINT IF EXISTS miriam_user_facts_category_check;

ALTER TABLE miriam_user_facts
    ADD CONSTRAINT miriam_user_facts_category_check CHECK (category IN (
        'goal', 'life_event', 'preference', 'habit', 'fear',
        'family', 'work', 'location', 'identity', 'financial_behavior'
    ));

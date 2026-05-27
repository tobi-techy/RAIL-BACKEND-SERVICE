-- Expand Miriam memory for income/deposit behavior and locale-aware delivery.

ALTER TABLE miriam_user_facts
    DROP CONSTRAINT IF EXISTS miriam_user_facts_category_check;

ALTER TABLE miriam_user_facts
    ADD CONSTRAINT miriam_user_facts_category_check CHECK (category IN (
        'goal', 'life_event', 'preference', 'habit', 'fear',
        'family', 'work', 'location', 'identity', 'financial_behavior',
        'income_pattern', 'deposit_cadence', 'salary_day',
        'freelance_pattern', 'family_support', 'currency_context',
        'risk_preference', 'stash_behavior'
    ));

ALTER TABLE miriam_tone_profiles
    ADD COLUMN IF NOT EXISTS locale_style VARCHAR(30) NOT NULL DEFAULT 'global';

ALTER TABLE miriam_tone_profiles
    DROP CONSTRAINT IF EXISTS miriam_tone_profiles_locale_style_check;

ALTER TABLE miriam_tone_profiles
    ADD CONSTRAINT miriam_tone_profiles_locale_style_check CHECK (locale_style IN (
        'global', 'nigeria', 'west_africa', 'diaspora_us',
        'diaspora_uk', 'europe', 'formal_global'
    ));

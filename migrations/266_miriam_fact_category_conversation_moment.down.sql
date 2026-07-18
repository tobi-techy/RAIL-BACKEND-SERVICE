-- Revert to migration 205's category list (without 'conversation_moment').

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

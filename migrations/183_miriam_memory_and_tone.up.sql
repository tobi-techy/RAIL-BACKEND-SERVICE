-- Miriam Memory System: persistent user facts + tone calibration

-- 1. User facts Miriam learns over time from conversations
CREATE TABLE IF NOT EXISTS miriam_user_facts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category VARCHAR(50) NOT NULL CHECK (category IN (
        'goal', 'life_event', 'preference', 'habit', 'fear',
        'family', 'work', 'location', 'identity', 'financial_behavior'
    )),
    fact TEXT NOT NULL,
    source VARCHAR(30) NOT NULL DEFAULT 'conversation' CHECK (source IN (
        'conversation', 'inferred', 'transaction_pattern', 'profile'
    )),
    confidence DECIMAL(3,2) NOT NULL DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    superseded_by UUID REFERENCES miriam_user_facts(id) ON DELETE SET NULL,
    first_observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_confirmed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_miriam_facts_user_cat ON miriam_user_facts(user_id, category)
    WHERE superseded_by IS NULL;
CREATE INDEX idx_miriam_facts_user_recent ON miriam_user_facts(user_id, last_confirmed_at DESC)
    WHERE superseded_by IS NULL;

-- 2. Tone profile per user — how Miriam should speak to this person
CREATE TABLE IF NOT EXISTS miriam_tone_profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    formality DECIMAL(3,2) NOT NULL DEFAULT 0.3 CHECK (formality >= 0 AND formality <= 1),
    directness DECIMAL(3,2) NOT NULL DEFAULT 0.7 CHECK (directness >= 0 AND directness <= 1),
    warmth DECIMAL(3,2) NOT NULL DEFAULT 0.7 CHECK (warmth >= 0 AND warmth <= 1),
    humor DECIMAL(3,2) NOT NULL DEFAULT 0.5 CHECK (humor >= 0 AND humor <= 1),
    brevity DECIMAL(3,2) NOT NULL DEFAULT 0.6 CHECK (brevity >= 0 AND brevity <= 1),
    preferred_name VARCHAR(100) DEFAULT '',
    language_style VARCHAR(30) NOT NULL DEFAULT 'standard' CHECK (language_style IN (
        'standard', 'casual', 'formal', 'pidgin_mix'
    )),
    sample_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

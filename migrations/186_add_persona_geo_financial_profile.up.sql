ALTER TABLE financial_profiles
    ADD COLUMN IF NOT EXISTS user_type VARCHAR(30) NOT NULL DEFAULT 'individual'
        CHECK (user_type IN ('individual', 'freelancer', 'founder', 'family', 'high_earner')),
    ADD COLUMN IF NOT EXISTS residence_country VARCHAR(2) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tax_country VARCHAR(2) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS earning_currency VARCHAR(10) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS spending_currency VARCHAR(10) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS family_support_country VARCHAR(2) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_financial_profiles_user_type
    ON financial_profiles (user_type);

CREATE INDEX IF NOT EXISTS idx_financial_profiles_residence_country
    ON financial_profiles (residence_country);

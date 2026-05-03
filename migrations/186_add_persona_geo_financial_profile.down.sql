DROP INDEX IF EXISTS idx_financial_profiles_residence_country;
DROP INDEX IF EXISTS idx_financial_profiles_user_type;

ALTER TABLE financial_profiles
    DROP COLUMN IF EXISTS family_support_country,
    DROP COLUMN IF EXISTS spending_currency,
    DROP COLUMN IF EXISTS earning_currency,
    DROP COLUMN IF EXISTS tax_country,
    DROP COLUMN IF EXISTS residence_country,
    DROP COLUMN IF EXISTS user_type;

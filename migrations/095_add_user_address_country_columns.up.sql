ALTER TABLE users
    ADD COLUMN IF NOT EXISTS country TEXT,
    ADD COLUMN IF NOT EXISTS address_street TEXT,
    ADD COLUMN IF NOT EXISTS address_city TEXT,
    ADD COLUMN IF NOT EXISTS address_state TEXT,
    ADD COLUMN IF NOT EXISTS address_postal_code TEXT,
    ADD COLUMN IF NOT EXISTS address_country TEXT;

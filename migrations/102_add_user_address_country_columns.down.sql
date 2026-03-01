ALTER TABLE users
    DROP COLUMN IF EXISTS address_country,
    DROP COLUMN IF EXISTS address_postal_code,
    DROP COLUMN IF EXISTS address_state,
    DROP COLUMN IF EXISTS address_city,
    DROP COLUMN IF EXISTS address_street,
    DROP COLUMN IF EXISTS country;

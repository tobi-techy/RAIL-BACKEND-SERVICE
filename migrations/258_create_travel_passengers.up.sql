-- Saved traveler profiles for Travu bookings. Users fill these once (in-app or
-- via Miriam in chat) and reuse them across bus/flight bookings. Flight
-- bookings require passport-level detail; bus bookings need only the basics.
-- PII: passport_number and contact fields are masked in logs.
CREATE TABLE IF NOT EXISTS travel_passengers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label VARCHAR(120),                               -- friendly name, e.g. "Me", "Wife"
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,        -- default traveler for this user
    passenger_type VARCHAR(10) NOT NULL DEFAULT 'Adult', -- Adult | Child | Infant
    -- Identity
    title VARCHAR(16),
    first_name VARCHAR(120) NOT NULL,
    middle_name VARCHAR(120),
    last_name VARCHAR(120) NOT NULL,
    dob VARCHAR(16),                                  -- MM/DD/YYYY
    sex VARCHAR(16),                                  -- Male | Female
    blood VARCHAR(8),
    -- Contact
    phone VARCHAR(32),
    email VARCHAR(255),
    -- Address
    address VARCHAR(255),
    city VARCHAR(120),
    postal_code VARCHAR(32),
    country VARCHAR(120),
    country_code VARCHAR(8),
    -- Passport (flights)
    passport_number VARCHAR(64),
    passport_country VARCHAR(120),
    passport_expiry VARCHAR(16),                      -- MM/DD/YYYY
    nationality VARCHAR(120),
    -- Next of kin (bus)
    next_of_kin VARCHAR(255),
    next_of_kin_phone VARCHAR(32),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_travel_passengers_user ON travel_passengers(user_id, created_at DESC);
-- At most one primary traveler per user.
CREATE UNIQUE INDEX idx_travel_passengers_primary ON travel_passengers(user_id) WHERE is_primary = TRUE;

CREATE SEQUENCE waitlist_position_seq START 1;

CREATE TABLE waitlist_users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT NOT NULL UNIQUE,
    full_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'waitlist' CHECK (status IN ('waitlist', 'invited', 'converted')),
    referral_code TEXT UNIQUE,
    referred_by UUID REFERENCES waitlist_users(id) ON DELETE SET NULL,
    position INTEGER NOT NULL DEFAULT nextval('waitlist_position_seq'),
    source TEXT NOT NULL DEFAULT 'website',
    converted_user_id UUID REFERENCES users(id),
    invited_at TIMESTAMP WITH TIME ZONE,
    converted_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

ALTER SEQUENCE waitlist_position_seq OWNED BY waitlist_users.position;

CREATE INDEX idx_waitlist_users_status ON waitlist_users(status);
CREATE INDEX idx_waitlist_users_referral_code ON waitlist_users(referral_code);
CREATE INDEX idx_waitlist_users_referred_by ON waitlist_users(referred_by);

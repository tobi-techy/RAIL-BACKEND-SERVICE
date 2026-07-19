-- Source of funds and employment data collected during onboarding
ALTER TABLE users ADD COLUMN IF NOT EXISTS employment_status TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS source_of_funds TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_purpose TEXT;

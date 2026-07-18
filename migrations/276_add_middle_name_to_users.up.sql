-- Add middle_name column to users table for OTP-only signup flow
ALTER TABLE users ADD COLUMN IF NOT EXISTS middle_name TEXT;

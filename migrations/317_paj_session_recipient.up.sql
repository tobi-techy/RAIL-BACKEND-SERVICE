-- Bind Paj sessions to the verified recipient so switching email/phone
-- forces a fresh OTP instead of silently running under another identity.
ALTER TABLE paj_sessions ADD COLUMN IF NOT EXISTS recipient_hash TEXT;

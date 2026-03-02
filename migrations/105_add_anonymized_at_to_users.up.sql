-- Add anonymized_at to track GDPR anonymization requests
-- GDPR compliance via user anonymization: clear PII (name, email, phone) but keep UUID as pseudonymous identifier
-- Financial records preserved via ON DELETE RESTRICT; personal data removed via application-layer anonymization
ALTER TABLE users ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMP WITH TIME ZONE;

-- Remove encryption features

-- Drop audit trigger
DROP TRIGGER IF EXISTS audit_encryption_keys ON encryption_keys;
DROP FUNCTION IF EXISTS audit_encryption_key_changes();

-- Remove encrypted columns
ALTER TABLE users DROP COLUMN IF EXISTS encrypted_ssn;
ALTER TABLE users DROP COLUMN IF EXISTS ssn_key_id;
ALTER TABLE users DROP COLUMN IF EXISTS encrypted_bank_details;
ALTER TABLE users DROP COLUMN IF EXISTS bank_key_id;

-- Drop indexes
DROP INDEX IF EXISTS idx_users_encrypted_ssn;

-- Drop encryption tables
DROP TABLE IF EXISTS encryption_keys;
DROP TABLE IF EXISTS encryption_status;

-- Drop encryption functions
DROP FUNCTION IF EXISTS encrypt_sensitive_data(text, text);
DROP FUNCTION IF EXISTS decrypt_sensitive_data(text, text);

-- Remove pgcrypto extension (if no longer needed)
-- DROP EXTENSION IF EXISTS pgcrypto; -- Commented out as it might be used elsewhere

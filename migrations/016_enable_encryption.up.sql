-- Enable Transparent Data Encryption (TDE) for PostgreSQL
-- Note: This requires PostgreSQL with pgcrypto extension and proper setup

-- Enable pgcrypto extension for encryption functions
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Create encryption key management table (for application-level encryption)
CREATE TABLE IF NOT EXISTS encryption_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key_id varchar(255) NOT NULL UNIQUE,
    encrypted_key text NOT NULL, -- Encrypted with master key
    algorithm varchar(50) NOT NULL DEFAULT 'AES-256-GCM',
    created_at timestamp with time zone DEFAULT now(),
    expires_at timestamp with time zone,
    is_active boolean DEFAULT true
);

-- Create index on active keys
CREATE INDEX idx_encryption_keys_active ON encryption_keys(is_active) WHERE is_active = true;

-- Add encryption status tracking
CREATE TABLE IF NOT EXISTS encryption_status (
    table_name varchar(255) PRIMARY KEY,
    encrypted_columns text[], -- JSON array of encrypted column names
    encryption_enabled boolean DEFAULT false,
    last_encryption_check timestamp with time zone DEFAULT now(),
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- Function to encrypt sensitive data
CREATE OR REPLACE FUNCTION encrypt_sensitive_data(input_text text, key_id text DEFAULT 'default')
RETURNS text AS $$
DECLARE
    encryption_key text;
BEGIN
    -- Get encryption key (in production, this would be retrieved from secure key management)
    SELECT encrypted_key INTO encryption_key
    FROM encryption_keys
    WHERE key_id = $2 AND is_active = true
    ORDER BY created_at DESC
    LIMIT 1;

    IF encryption_key IS NULL THEN
        RAISE EXCEPTION 'No active encryption key found for key_id: %', key_id;
    END IF;

    -- Use pgcrypto for encryption (simplified - in production use proper key management)
    RETURN encode(encrypt(input_text::bytea, encryption_key::bytea, 'aes'), 'base64');
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to decrypt sensitive data
CREATE OR REPLACE FUNCTION decrypt_sensitive_data(encrypted_text text, key_id text DEFAULT 'default')
RETURNS text AS $$
DECLARE
    encryption_key text;
    decrypted_data bytea;
BEGIN
    -- Get encryption key
    SELECT encrypted_key INTO encryption_key
    FROM encryption_keys
    WHERE key_id = $2 AND is_active = true
    ORDER BY created_at DESC
    LIMIT 1;

    IF encryption_key IS NULL THEN
        RAISE EXCEPTION 'No active encryption key found for key_id: %', key_id;
    END IF;

    -- Decrypt data
    decrypted_data := decrypt(decode(encrypted_text, 'base64'), encryption_key::bytea, 'aes');
    RETURN convert_from(decrypted_data, 'UTF8');
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Add encrypted fields to existing tables (optional - for extra sensitive data)
-- Note: wallet_sets.entity_secret_ciphertext is already encrypted

-- Add encrypted SSN field to users table (if needed for KYC)
ALTER TABLE users ADD COLUMN IF NOT EXISTS encrypted_ssn text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS ssn_key_id text DEFAULT 'user-data';

-- Add encrypted bank account details to users table (if needed)
ALTER TABLE users ADD COLUMN IF NOT EXISTS encrypted_bank_details text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS bank_key_id text DEFAULT 'financial-data';

-- Create partial indexes for encrypted fields (non-concurrent due to transaction)
CREATE INDEX IF NOT EXISTS idx_users_encrypted_ssn
    ON users(encrypted_ssn)
    WHERE encrypted_ssn IS NOT NULL;

-- Insert default encryption keys (in production, these would be managed by AWS KMS or similar)
INSERT INTO encryption_keys (key_id, encrypted_key, algorithm)
VALUES
    ('default', 'your-master-encryption-key-here', 'AES-256-GCM'),
    ('user-data', 'user-data-encryption-key-here', 'AES-256-GCM'),
    ('financial-data', 'financial-data-encryption-key-here', 'AES-256-GCM'),
    ('wallet-data', 'wallet-data-encryption-key-here', 'AES-256-GCM')
ON CONFLICT (key_id) DO NOTHING;

-- Update encryption status
INSERT INTO encryption_status (table_name, encrypted_columns, encryption_enabled)
VALUES
    ('users', ARRAY['encrypted_ssn', 'encrypted_bank_details'], true),
    ('wallet_sets', ARRAY['entity_secret_ciphertext'], true)
ON CONFLICT (table_name) DO UPDATE SET
    encrypted_columns = EXCLUDED.encrypted_columns,
    encryption_enabled = EXCLUDED.encryption_enabled,
    updated_at = now();

-- Create audit trigger for encryption key changes
CREATE OR REPLACE FUNCTION audit_encryption_key_changes()
RETURNS trigger AS $$
BEGIN
    INSERT INTO audit_logs (action, entity, before, after, occurred_at)
    VALUES (
        CASE
            WHEN TG_OP = 'INSERT' THEN 'encryption_key_created'
            WHEN TG_OP = 'UPDATE' THEN 'encryption_key_updated'
            WHEN TG_OP = 'DELETE' THEN 'encryption_key_deleted'
        END,
        'encryption_keys',
        CASE WHEN TG_OP != 'INSERT' THEN row_to_json(OLD)::text ELSE NULL END,
        CASE WHEN TG_OP != 'DELETE' THEN row_to_json(NEW)::text ELSE NULL END,
        now()
    );
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_encryption_keys
    AFTER INSERT OR UPDATE OR DELETE ON encryption_keys
    FOR EACH ROW EXECUTE FUNCTION audit_encryption_key_changes();

-- Note: In production, grant appropriate permissions to application roles
-- GRANT SELECT, INSERT, UPDATE ON encryption_keys TO application_role;
-- GRANT SELECT, INSERT, UPDATE ON encryption_status TO application_role;

-- Note: In production, encryption keys should be managed by AWS KMS, Azure Key Vault, or similar
-- This implementation provides application-level encryption as an additional security layer
-- Database-level TDE should be enabled at the RDS level in AWS

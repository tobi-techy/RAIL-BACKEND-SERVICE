DROP TRIGGER IF EXISTS update_social_accounts_updated_at ON social_accounts;
DROP INDEX IF EXISTS idx_webauthn_credentials_credential_id;
DROP INDEX IF EXISTS idx_webauthn_credentials_user_id;
DROP TABLE IF EXISTS webauthn_credentials;
DROP INDEX IF EXISTS idx_social_accounts_provider_id;
DROP INDEX IF EXISTS idx_social_accounts_user_id;
DROP TABLE IF EXISTS social_accounts;

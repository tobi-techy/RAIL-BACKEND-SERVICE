DROP TABLE IF EXISTS vault_health;
DROP TABLE IF EXISTS vault_enroll_failures;
DROP TABLE IF EXISTS vault_skips;
DROP TABLE IF EXISTS vault_tiers;
DROP INDEX IF EXISTS idx_vault_lots_vault_payment_key;
DROP INDEX IF EXISTS idx_vault_penalties_vault_created;
DROP INDEX IF EXISTS idx_vault_auth_vault_created;
ALTER TABLE retirement_vaults DROP CONSTRAINT IF EXISTS retirement_vaults_status_check;
ALTER TABLE retirement_vaults
    ADD CONSTRAINT retirement_vaults_status_check
    CHECK (status IN ('active', 'paused', 'closed'));

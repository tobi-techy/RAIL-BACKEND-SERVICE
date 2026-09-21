DROP INDEX IF EXISTS idx_investment_enrollments_vault;
ALTER TABLE investment_enrollments DROP COLUMN IF EXISTS vault_id;

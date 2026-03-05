-- WARNING: This rollback will permanently delete bank_name and beneficiary_name data.
-- Run the following backup command before executing this migration if you need to preserve the data:
-- CREATE TABLE virtual_accounts_bank_metadata_backup AS SELECT id, user_id, currency, bank_name, beneficiary_name FROM virtual_accounts WHERE bank_name IS NOT NULL OR beneficiary_name IS NOT NULL;

-- Remove bank_name and beneficiary_name columns from virtual_accounts table
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS bank_name;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS beneficiary_name;

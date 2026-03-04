-- Remove bank_name and beneficiary_name columns from virtual_accounts table
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS bank_name;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS beneficiary_name;

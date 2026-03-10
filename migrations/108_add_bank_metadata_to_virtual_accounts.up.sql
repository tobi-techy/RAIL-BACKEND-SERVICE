-- Add bank_name and beneficiary_name columns to virtual_accounts table
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS bank_name VARCHAR(255) DEFAULT '';
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS beneficiary_name VARCHAR(255) DEFAULT '';

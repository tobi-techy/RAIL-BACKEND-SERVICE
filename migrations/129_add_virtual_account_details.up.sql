ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS bank_address TEXT DEFAULT '';
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS beneficiary_address TEXT DEFAULT '';
ALTER TABLE virtual_accounts ADD COLUMN IF NOT EXISTS payment_rails TEXT[] DEFAULT '{}';

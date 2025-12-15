-- Remove account_type field from managed_wallets table
ALTER TABLE managed_wallets DROP COLUMN IF EXISTS account_type;

-- Add full routing_number column for Bridge integration

ALTER TABLE bank_accounts ADD COLUMN IF NOT EXISTS routing_number VARCHAR(9);

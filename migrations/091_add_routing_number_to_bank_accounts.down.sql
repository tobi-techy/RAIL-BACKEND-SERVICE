-- +migrate Down
ALTER TABLE bank_accounts DROP COLUMN IF EXISTS routing_number;

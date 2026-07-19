DROP INDEX IF EXISTS idx_virtual_accounts_provider;
DROP INDEX IF EXISTS idx_virtual_accounts_graph_account_id;

ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS bank_code;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS graph_account_id;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS graph_person_id;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS provider;

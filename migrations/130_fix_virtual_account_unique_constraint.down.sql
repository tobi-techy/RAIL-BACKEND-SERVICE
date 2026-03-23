DROP INDEX IF EXISTS idx_virtual_accounts_user_currency;
ALTER TABLE virtual_accounts ADD CONSTRAINT virtual_accounts_due_account_id_key UNIQUE (bridge_customer_id);

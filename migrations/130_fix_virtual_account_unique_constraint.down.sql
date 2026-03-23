DROP INDEX IF EXISTS idx_virtual_accounts_user_currency;
ALTER TABLE virtual_accounts ALTER COLUMN alpaca_account_id SET NOT NULL;
ALTER TABLE virtual_accounts ALTER COLUMN account_number SET NOT NULL;
ALTER TABLE virtual_accounts ADD CONSTRAINT virtual_accounts_account_number_key UNIQUE (account_number);
ALTER TABLE virtual_accounts ADD CONSTRAINT virtual_accounts_user_id_alpaca_account_id_key UNIQUE (user_id, alpaca_account_id);
ALTER TABLE virtual_accounts ADD CONSTRAINT virtual_accounts_due_account_id_key UNIQUE (bridge_customer_id);

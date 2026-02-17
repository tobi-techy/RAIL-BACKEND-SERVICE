ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS fk_withdrawals_bank_account;

DROP INDEX IF EXISTS idx_bank_accounts_user_id;
DROP INDEX IF EXISTS idx_bank_accounts_user_id_primary;
DROP INDEX IF EXISTS idx_bank_accounts_bridge_recipient_id;
DROP TABLE IF EXISTS bank_accounts;

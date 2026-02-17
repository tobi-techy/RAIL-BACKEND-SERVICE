-- Remove new columns and restore old structure

DROP INDEX IF EXISTS idx_withdrawals_user_id_type;
DROP INDEX IF EXISTS idx_withdrawals_status_type;
DROP INDEX IF EXISTS idx_withdrawals_circle_wallet_id;
DROP INDEX IF EXISTS idx_withdrawals_bank_account_id;

ALTER TABLE withdrawals DROP COLUMN IF EXISTS withdrawal_type;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS currency;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS source_account;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS circle_wallet_id;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS destination_type;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS bank_account_id;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS fee_amount;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS fee_currency;

ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS alpaca_account_id VARCHAR(255);
ALTER TABLE withdrawals ADD COLUMN IF NOT EXISTS alpaca_journal_id VARCHAR(255);

-- Remove all Due-related columns and tables, rename to Bridge where needed

-- Users table: rename due columns to bridge (idempotent)
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'due_account_id'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'bridge_customer_id'
	) THEN
		ALTER TABLE users RENAME COLUMN due_account_id TO bridge_customer_id;
	END IF;

	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'due_kyc_status'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'bridge_kyc_status'
	) THEN
		ALTER TABLE users RENAME COLUMN due_kyc_status TO bridge_kyc_status;
	END IF;

	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'due_kyc_link'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'bridge_kyc_link'
	) THEN
		ALTER TABLE users RENAME COLUMN due_kyc_link TO bridge_kyc_link;
	END IF;
END $$;

DROP INDEX IF EXISTS idx_users_due_account_id;
CREATE INDEX IF NOT EXISTS idx_users_bridge_customer_id ON users(bridge_customer_id);

-- Virtual accounts: rename due_account_id to bridge_customer_id, drop due_recipient_id (idempotent)
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'virtual_accounts' AND column_name = 'due_account_id'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'virtual_accounts' AND column_name = 'bridge_customer_id'
	) THEN
		ALTER TABLE virtual_accounts RENAME COLUMN due_account_id TO bridge_customer_id;
	END IF;
END $$;

ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS due_recipient_id;

-- Withdrawals: rename due columns to bridge (idempotent)
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'withdrawals' AND column_name = 'due_transfer_id'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'withdrawals' AND column_name = 'bridge_transfer_id'
	) THEN
		ALTER TABLE withdrawals RENAME COLUMN due_transfer_id TO bridge_transfer_id;
	END IF;

	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'withdrawals' AND column_name = 'due_recipient_id'
	) AND NOT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'withdrawals' AND column_name = 'bridge_recipient_id'
	) THEN
		ALTER TABLE withdrawals RENAME COLUMN due_recipient_id TO bridge_recipient_id;
	END IF;
END $$;
UPDATE withdrawals SET status = 'bridge_processing' WHERE status = 'due_processing';

-- Drop Due webhook events table
DROP TABLE IF EXISTS due_webhook_events;

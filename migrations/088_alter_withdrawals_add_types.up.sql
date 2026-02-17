-- Withdrawal table changes for new withdrawal architecture
-- Add new fields for crypto/fiat withdrawals, remove Alpaca-specific fields

ALTER TABLE withdrawals
ADD COLUMN IF NOT EXISTS withdrawal_type VARCHAR(20) NOT NULL DEFAULT 'crypto',
ADD COLUMN IF NOT EXISTS currency VARCHAR(10) NOT NULL DEFAULT 'USDC',
ADD COLUMN IF NOT EXISTS source_account VARCHAR(20) NOT NULL DEFAULT 'spending_balance',
ADD COLUMN IF NOT EXISTS circle_wallet_id VARCHAR(255),
ADD COLUMN IF NOT EXISTS destination_type VARCHAR(20) NOT NULL DEFAULT 'crypto_wallet',
ADD COLUMN IF NOT EXISTS bank_account_id UUID,
ADD COLUMN IF NOT EXISTS fee_amount DECIMAL(36,18) DEFAULT 0,
ADD COLUMN IF NOT EXISTS fee_currency VARCHAR(10) DEFAULT 'USDC';

-- Rename bridge_processing to processing (if both columns exist)
-- Note: bridge_processing may not exist in older tables, so we skip if it doesn't

-- Remove Alpaca-specific columns (if they exist)
ALTER TABLE withdrawals DROP COLUMN IF EXISTS alpaca_account_id;
ALTER TABLE withdrawals DROP COLUMN IF EXISTS alpaca_journal_id;

-- Add index for faster queries
CREATE INDEX IF NOT EXISTS idx_withdrawals_user_id_type ON withdrawals(user_id, withdrawal_type);
CREATE INDEX IF NOT EXISTS idx_withdrawals_status_type ON withdrawals(status, withdrawal_type);
CREATE INDEX IF NOT EXISTS idx_withdrawals_circle_wallet_id ON withdrawals(circle_wallet_id) WHERE circle_wallet_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_withdrawals_bank_account_id ON withdrawals(bank_account_id) WHERE bank_account_id IS NOT NULL;

-- Add deposit_type column to deposits table for unified deposit tracking
-- Allows distinguishing between crypto and fiat deposits in a single table

ALTER TABLE deposits ADD COLUMN IF NOT EXISTS deposit_type VARCHAR(10) NOT NULL DEFAULT 'crypto'
    CHECK (deposit_type IN ('crypto', 'fiat'));

-- Index for filtering by deposit type
CREATE INDEX IF NOT EXISTS idx_deposits_deposit_type ON deposits (deposit_type);

-- Update existing deposits: any with a virtual_account_id are fiat
UPDATE deposits SET deposit_type = 'fiat' WHERE virtual_account_id IS NOT NULL;

-- Add account_type field to managed_wallets table for developer-controlled wallets
-- This field distinguishes between EOA (Externally Owned Account) and SCA (Smart Contract Account) wallets

-- Add account_type column if it doesn't exist
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'managed_wallets' 
        AND column_name = 'account_type'
    ) THEN
        ALTER TABLE managed_wallets 
        ADD COLUMN account_type VARCHAR(10) DEFAULT 'EOA' CHECK (
            account_type IN ('EOA', 'SCA')
        );
    END IF;
END $$;

-- Update existing records to have EOA as default account type
UPDATE managed_wallets 
SET account_type = 'EOA' 
WHERE account_type IS NULL;

-- Add comment to the column
COMMENT ON COLUMN managed_wallets.account_type IS 'Account type: EOA (Externally Owned Account) or SCA (Smart Contract Account) for developer-controlled wallets';

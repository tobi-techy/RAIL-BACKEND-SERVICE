-- Restrict managed_wallets to only support SOL-DEVNET chain
-- Step 1: Remove the existing constraint
ALTER TABLE managed_wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_chain;

-- Step 2: Delete all wallets that are NOT on SOL-DEVNET
-- This is a destructive operation - existing wallets on other chains will be removed
DELETE FROM managed_wallets WHERE chain != 'SOL-DEVNET';

-- Step 3: Add the new constraint that only allows SOL-DEVNET
ALTER TABLE managed_wallets
    ADD CONSTRAINT chk_wallet_chain CHECK (
        chain IN ('SOL-DEVNET')
    );

-- Add comment explaining the constraint
COMMENT ON CONSTRAINT chk_wallet_chain ON managed_wallets IS 'Only SOL-DEVNET chain is supported';

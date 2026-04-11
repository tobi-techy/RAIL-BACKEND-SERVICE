-- Fix CHECK constraint mismatches between Go constants and DB constraints

-- 0. onboarding_status: migration 140 used ALTER TYPE on a TEXT column (no-op),
--    so basic_complete was never actually added to the CHECK constraints
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_onboarding_status;
ALTER TABLE users ADD CONSTRAINT chk_onboarding_status
    CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check;
ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check
    CHECK (onboarding_status IN ('started', 'basic_complete', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

-- 1. deposits.status: add 'initiated' (defined in DepositStatusInitiated)
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'initiated', 'pending', 'confirmed', 'failed', 'expired', 'timeout',
        'off_ramp_initiated', 'off_ramp_completed', 'broker_funded'
    ));

-- 2. card_transactions.status: add 'timeout' (defined in CardTxStatusTimeout)
ALTER TABLE card_transactions DROP CONSTRAINT IF EXISTS card_transactions_status_check;
ALTER TABLE card_transactions ADD CONSTRAINT card_transactions_status_check
    CHECK (status IN ('pending', 'completed', 'declined', 'reversed', 'timeout'));

-- 3. deposits.chain: add all chains defined in Go Chain constants
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_chain_check
    CHECK (chain IN (
        'SOL', 'ETH', 'MATIC', 'CELO', 'BASE', 'AVAX',
        'ARB', 'OP', 'STRK', 'BNB', 'fiat'
    ));

-- Revert to previous constraint values

ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_onboarding_status;
ALTER TABLE users ADD CONSTRAINT chk_onboarding_status
    CHECK (onboarding_status IN ('started', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_onboarding_status_check;
ALTER TABLE users ADD CONSTRAINT users_onboarding_status_check
    CHECK (onboarding_status IN ('started', 'kyc_pending', 'kyc_approved', 'kyc_rejected', 'wallets_pending', 'completed'));

ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'pending', 'confirmed', 'failed', 'expired', 'timeout',
        'off_ramp_initiated', 'off_ramp_completed', 'broker_funded'
    ));

ALTER TABLE card_transactions DROP CONSTRAINT IF EXISTS card_transactions_status_check;
ALTER TABLE card_transactions ADD CONSTRAINT card_transactions_status_check
    CHECK (status IN ('pending', 'completed', 'declined', 'reversed'));

ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_chain_check
    CHECK (chain IN (
        'SOL', 'SOL-DEVNET', 'MATIC', 'MATIC-AMOY',
        'AVAX', 'AVAX-FUJI', 'BASE', 'BASE-SEPOLIA'
    ));

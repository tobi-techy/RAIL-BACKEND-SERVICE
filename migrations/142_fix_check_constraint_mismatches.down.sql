-- Revert to previous constraint values

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

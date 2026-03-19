ALTER TABLE p2p_transfers
    DROP CONSTRAINT IF EXISTS p2p_transfers_status_check;

ALTER TABLE p2p_transfers
    ADD CONSTRAINT p2p_transfers_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'claimed', 'expired', 'cancelled'));

-- Optimize recurring expense detection query performance
CREATE INDEX IF NOT EXISTS idx_card_transactions_recurring
    ON card_transactions (user_id, created_at)
    WHERE type = 'capture' AND status = 'completed';

CREATE INDEX IF NOT EXISTS idx_p2p_transfers_recurring
    ON p2p_transfers (sender_id, created_at)
    WHERE status = 'completed';

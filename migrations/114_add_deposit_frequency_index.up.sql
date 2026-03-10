CREATE INDEX IF NOT EXISTS idx_deposits_user_status_created
    ON deposits(user_id, status, created_at DESC)
    WHERE status = 'broker_funded';

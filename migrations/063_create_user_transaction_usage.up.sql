-- User Transaction Usage for tracking deposit/withdrawal limits
CREATE TABLE IF NOT EXISTS user_transaction_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    
    -- Deposit tracking
    daily_deposit_used DECIMAL(20, 8) DEFAULT 0,
    daily_deposit_reset_at TIMESTAMP NOT NULL,
    monthly_deposit_used DECIMAL(20, 8) DEFAULT 0,
    monthly_deposit_reset_at TIMESTAMP NOT NULL,
    
    -- Withdrawal tracking
    daily_withdrawal_used DECIMAL(20, 8) DEFAULT 0,
    daily_withdrawal_reset_at TIMESTAMP NOT NULL,
    monthly_withdrawal_used DECIMAL(20, 8) DEFAULT 0,
    monthly_withdrawal_reset_at TIMESTAMP NOT NULL,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_user_transaction_usage_user_id ON user_transaction_usage(user_id);
CREATE INDEX idx_user_transaction_usage_daily_reset ON user_transaction_usage(daily_deposit_reset_at, daily_withdrawal_reset_at);

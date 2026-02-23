-- Create stash_transfers table for transferring funds from stash to spending

CREATE TABLE IF NOT EXISTS stash_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(36,18) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Indexes for faster queries
CREATE INDEX IF NOT EXISTS idx_stash_transfers_user_id ON stash_transfers(user_id);
CREATE INDEX IF NOT EXISTS idx_stash_transfers_status ON stash_transfers(status);

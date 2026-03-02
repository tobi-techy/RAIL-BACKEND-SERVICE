-- P2P Transfers table for Cash App-style money transfers
-- Supports RailTag, email, and phone transfers with pending/claim flow

CREATE TABLE p2p_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Sender (always a Rail user)
    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    
    -- Recipient: either existing user OR pending claim
    recipient_id UUID REFERENCES users(id) ON DELETE SET NULL,  -- NULL until claimed by new user
    recipient_identifier VARCHAR(255) NOT NULL,  -- railtag, email, or phone
    identifier_type VARCHAR(20) NOT NULL CHECK (identifier_type IN ('railtag', 'email', 'phone')),
    
    -- Transfer details
    amount DECIMAL(18,2) NOT NULL CHECK (amount > 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    note VARCHAR(255),
    
    -- Status: pending (awaiting claim), completed (instant to existing user), 
    --         claimed (new user signed up), expired (14 days passed), cancelled (sender cancelled)
    status VARCHAR(20) NOT NULL DEFAULT 'pending' 
        CHECK (status IN ('pending', 'completed', 'claimed', 'expired', 'cancelled')),
    
    -- Claim tracking for non-users
    claim_token VARCHAR(64) UNIQUE,  -- Secure token for claim link
    claim_link_sent_at TIMESTAMP WITH TIME ZONE,
    reminder_sent_at TIMESTAMP WITH TIME ZONE,
    
    -- Resolution timestamps
    completed_at TIMESTAMP WITH TIME ZONE,  -- When transfer completed (instant or claimed)
    cancelled_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,  -- 14 days from creation
    
    -- Audit
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_p2p_transfers_sender_id ON p2p_transfers(sender_id);
CREATE INDEX idx_p2p_transfers_recipient_id ON p2p_transfers(recipient_id) WHERE recipient_id IS NOT NULL;
CREATE INDEX idx_p2p_transfers_claim_token ON p2p_transfers(claim_token) WHERE claim_token IS NOT NULL;
CREATE INDEX idx_p2p_transfers_recipient_identifier ON p2p_transfers(identifier_type, recipient_identifier);
CREATE INDEX idx_p2p_transfers_status ON p2p_transfers(status) WHERE status = 'pending';
CREATE INDEX idx_p2p_transfers_expires_at ON p2p_transfers(expires_at) WHERE status = 'pending';

-- Recent recipients for quick access (like Cash App's recent list)
CREATE TABLE p2p_recent_recipients (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    last_sent_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    send_count INT NOT NULL DEFAULT 1,
    PRIMARY KEY (user_id, recipient_id)
);

CREATE INDEX idx_p2p_recent_recipients_user_id ON p2p_recent_recipients(user_id, last_sent_at DESC);

-- Trigger to update updated_at
CREATE OR REPLACE FUNCTION update_p2p_transfers_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_p2p_transfers_updated_at
    BEFORE UPDATE ON p2p_transfers
    FOR EACH ROW
    EXECUTE FUNCTION update_p2p_transfers_updated_at();

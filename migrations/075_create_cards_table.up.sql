-- Create cards table for Bridge card system
CREATE TABLE IF NOT EXISTS cards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bridge_card_id VARCHAR(255) NOT NULL UNIQUE,
    bridge_customer_id VARCHAR(255) NOT NULL,
    type VARCHAR(20) NOT NULL CHECK (type IN ('virtual', 'physical')),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'frozen', 'cancelled')),
    last_4 VARCHAR(4) NOT NULL,
    expiry VARCHAR(7) NOT NULL,
    card_image_url TEXT,
    currency VARCHAR(10) NOT NULL DEFAULT 'usd',
    chain VARCHAR(50) NOT NULL,
    wallet_address VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create card_transactions table for transaction history
CREATE TABLE IF NOT EXISTS card_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    card_id UUID NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bridge_trans_id VARCHAR(255) NOT NULL UNIQUE,
    type VARCHAR(20) NOT NULL CHECK (type IN ('authorization', 'capture', 'refund', 'reversal')),
    amount DECIMAL(20, 8) NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'usd',
    merchant_name VARCHAR(255),
    merchant_category VARCHAR(100),
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'declined', 'reversed')),
    decline_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes for efficient queries
CREATE INDEX idx_cards_user_id ON cards(user_id);
CREATE INDEX idx_cards_bridge_card_id ON cards(bridge_card_id);
CREATE INDEX idx_cards_status ON cards(status);
CREATE INDEX idx_card_transactions_card_id ON card_transactions(card_id);
CREATE INDEX idx_card_transactions_user_id ON card_transactions(user_id);
CREATE INDEX idx_card_transactions_bridge_trans_id ON card_transactions(bridge_trans_id);
CREATE INDEX idx_card_transactions_created_at ON card_transactions(created_at DESC);

-- Trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_cards_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cards_updated_at_trigger
    BEFORE UPDATE ON cards
    FOR EACH ROW
    EXECUTE FUNCTION update_cards_updated_at();

CREATE TRIGGER card_transactions_updated_at_trigger
    BEFORE UPDATE ON card_transactions
    FOR EACH ROW
    EXECUTE FUNCTION update_cards_updated_at();

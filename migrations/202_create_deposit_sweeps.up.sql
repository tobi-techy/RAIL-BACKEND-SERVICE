-- Tracks auto-sweep intents that bridge non-Solana Circle deposits to the user's Solana wallet.
CREATE TABLE deposit_sweeps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deposit_id UUID NOT NULL REFERENCES deposits(id),
    user_id UUID NOT NULL REFERENCES users(id),
    source_chain TEXT NOT NULL,
    amount NUMERIC(20,6) NOT NULL,
    intent_address TEXT,
    chainrails_intent_id INT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed')),
    tx_hash TEXT,
    error_message TEXT,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_deposit_sweeps_status ON deposit_sweeps(status) WHERE status IN ('pending', 'in_progress');
CREATE UNIQUE INDEX idx_deposit_sweeps_deposit_id ON deposit_sweeps(deposit_id);
CREATE INDEX idx_deposit_sweeps_user_id ON deposit_sweeps(user_id);
CREATE INDEX idx_deposit_sweeps_intent_address ON deposit_sweeps(intent_address) WHERE intent_address IS NOT NULL;

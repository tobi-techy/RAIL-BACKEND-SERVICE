CREATE TABLE treasury_positions (
    id UUID PRIMARY KEY,
    operation VARCHAR(20) NOT NULL,  -- 'deposit' or 'withdrawal'
    amount NUMERIC(36,18) NOT NULL,
    tx_hash VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_treasury_positions_created ON treasury_positions(created_at DESC);

-- Track the last distributed yield high-water mark to survive process restarts.
CREATE TABLE yield_state (
    key VARCHAR(50) PRIMARY KEY,
    value NUMERIC(36,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO yield_state (key, value) VALUES ('last_distributed_yield', 0);

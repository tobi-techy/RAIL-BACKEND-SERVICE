-- Travu bus & flight booking orders. Unlike Airbills (which settles per-order in
-- USDC on Solana), Travu debits Rail's prefunded NGN float on the Travu
-- dashboard. Rail charges the user in USDC via a ledger hold at the live FX
-- rate and reconciles against the Travu wallet balance. This table tracks every
-- booking for reconciliation, ticket delivery, and recovery.
CREATE TABLE IF NOT EXISTS travel_orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode VARCHAR(10) NOT NULL,                        -- bus | flight
    provider VARCHAR(64),                             -- operator short_name (e.g. GUO, ABC)
    status VARCHAR(20) NOT NULL DEFAULT 'held',       -- held, booked, completed, failed, reversed
    -- Travu identifiers
    travu_order_id VARCHAR(128),                      -- order_id from the booking receipt
    travu_order_number VARCHAR(128),                  -- order_number from the booking receipt
    booking_id VARCHAR(128),                          -- flight tentative booking_id
    pnr VARCHAR(32),                                  -- flight PNR
    -- Trip details (for the receipt / ticket)
    route VARCHAR(255),                               -- narration, e.g. "UMUAHIA TO LAGOS"
    departure_terminal VARCHAR(255),
    destination_terminal VARCHAR(255),
    trip_date VARCHAR(32),                            -- travel date as returned by Travu
    seats VARCHAR(128),                               -- comma-separated seat numbers (bus)
    passengers JSONB,                                 -- snapshot of the passengers booked
    receipt JSONB,                                    -- full normalized OrderReceipt for audit / re-render
    -- Amounts
    amount_ngn DECIMAL(20, 2) NOT NULL,               -- fare in NGN
    amount_usdc DECIMAL(20, 8),                       -- fare converted to USDC at booking
    rail_fee_usdc DECIMAL(20, 8),                     -- Rail's service fee included in the hold
    hold_amount DECIMAL(20, 8),                       -- total USDC debited (fare + rail fee)
    rate DECIMAL(20, 4),                              -- NGN per USD applied
    -- Ticket delivery + idempotency tracking
    ticket_delivered BOOLEAN NOT NULL DEFAULT FALSE,  -- whether the PDF ticket reached the user
    deposit_id UUID UNIQUE,                           -- idempotency claim slot for hold/reversal
    failure_reason VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_travel_orders_user ON travel_orders(user_id, created_at DESC);
CREATE INDEX idx_travel_orders_status ON travel_orders(status) WHERE status IN ('held', 'booked', 'completed');
CREATE INDEX idx_travel_orders_travu_order ON travel_orders(travu_order_id);
CREATE INDEX idx_travel_orders_undelivered ON travel_orders(status) WHERE status = 'completed' AND ticket_delivered = FALSE;

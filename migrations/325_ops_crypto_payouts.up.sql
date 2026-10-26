-- Operator-driven crypto payouts: move money out of a Rail-managed Circle
-- wallet and into a destination wallet WITHOUT touching the ledger.
--
-- Built for the case where crypto arrived on-chain but never reached the
-- ledger, and the user cannot authenticate, so neither a ledger credit nor a
-- user withdrawal is available. The money never enters Rail's books: no
-- ledger_transactions, no ledger entries, no balance changes. These rows are
-- the audit trail for what left, where it went, and who authorised it.
--
-- Suppression is the safety property. Before anything moves, a `deposits` row
-- with status 'bridged' is written for the source transaction. That row is not
-- a ledger credit — it exists solely because FundingService.ProcessCircleDeposit
-- returns early for any existing deposit row that is not 'pending'. Because
-- both the Circle webhook and /internal/deposits/backfill go through that
-- function, neither can credit the same money after it has been bridged out.
-- The row is deleted again if funding definitively fails before money moves,
-- so the normal credit path is still available as a fallback.
--
-- Deposits.tx_hash is UNIQUE in the base schema, which is the second half of
-- the guard: inserting the suppression row and the webhook's pending row for
-- the same transaction can never both succeed.

CREATE TABLE ops_crypto_payouts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    source_chain TEXT NOT NULL,
    source_wallet_id UUID,
    source_circle_wallet_id TEXT NOT NULL,
    source_address TEXT NOT NULL,
    source_circle_tx_id TEXT NOT NULL,
    source_tx_hash TEXT NOT NULL DEFAULT '',
    destination_chain TEXT NOT NULL,
    destination_address TEXT NOT NULL,
    amount NUMERIC(20,6) NOT NULL CHECK (amount > 0),
    funding_amount NUMERIC(20,6) CHECK (funding_amount >= 0),
    fee_amount NUMERIC(20,6) CHECK (fee_amount >= 0),
    chainrails_intent_id INT,
    intent_address TEXT,
    circle_transfer_id TEXT,
    circle_tx_hash TEXT,
    status TEXT NOT NULL CHECK (status IN (
        'created', 'intent_creating', 'intent_created', 'funding',
        'funded', 'completed', 'funding_uncertain', 'failed', 'refunded'
    )),
    suppression_deposit_id UUID REFERENCES deposits(id),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    reason TEXT NOT NULL,
    actor TEXT NOT NULL,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

-- One payout per source transfer, forever. A retry resumes the same row and
-- re-funds the same ChainRails intent under the same Circle idempotency key;
-- it never opens a second path to move the same money.
CREATE UNIQUE INDEX idx_ops_crypto_payouts_source_tx
    ON ops_crypto_payouts(source_circle_tx_id);

CREATE INDEX idx_ops_crypto_payouts_user ON ops_crypto_payouts(user_id, created_at DESC);

-- Rows that may still move money. Kept narrow: this is the set an operator
-- has to chase when something is stuck mid-flight.
CREATE INDEX idx_ops_crypto_payouts_in_flight ON ops_crypto_payouts(status, updated_at)
    WHERE status IN ('created', 'intent_creating', 'intent_created', 'funding', 'funding_uncertain');

-- deposits.status gains 'bridged': the source transaction was paid out
-- off-ledger. Deliberately NOT 'pending' (that would trigger a ledger
-- reconcile) and NOT 'confirmed' (that would claim a ledger credit exists).
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_status_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_status_check
    CHECK (status IN (
        'initiated', 'pending', 'confirmed', 'failed', 'expired', 'timeout',
        'off_ramp_initiated', 'off_ramp_completed', 'broker_funded', 'bridged'
    ));

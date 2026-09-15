-- Investment (Glider) ledger, authorization and audit tables
-- Funding transfers are the ledger + on-chain leg that moves USDC into a
-- portfolio deposit account. Signature requests are the replay-protection
-- anchors for two-stage owner authorizations, confirmations bind a validated
-- payload to an explicit user "yes", and operations mirror provider async work.

CREATE TABLE IF NOT EXISTS investment_funding_transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID NOT NULL REFERENCES investment_enrollments(id) ON DELETE CASCADE,
    direction VARCHAR(16) NOT NULL DEFAULT 'deposit',
    amount_usd NUMERIC(20, 8) NOT NULL,
    asset VARCHAR(16) NOT NULL DEFAULT 'USDC',
    source_account VARCHAR(32) NOT NULL DEFAULT '',
    destination_account_id TEXT NOT NULL,
    ledger_transaction_id UUID,
    onchain_tx_ref TEXT NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'PENDING',
    idempotency_key TEXT,
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investment_funding_user ON investment_funding_transfers (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_investment_funding_enrollment ON investment_funding_transfers (enrollment_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_funding_idempotency ON investment_funding_transfers (idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS investment_signature_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID REFERENCES investment_enrollments(id) ON DELETE CASCADE,
    flow VARCHAR(32) NOT NULL,
    provider_flow_id TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(24) NOT NULL DEFAULT 'prepared',
    expires_at TIMESTAMP WITH TIME ZONE,
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investment_signature_user ON investment_signature_requests (user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_signature_flow ON investment_signature_requests (flow, provider_flow_id) WHERE provider_flow_id <> '';

CREATE TABLE IF NOT EXISTS investment_confirmations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    action VARCHAR(64) NOT NULL,
    action_hash TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    verdict VARCHAR(32) NOT NULL,
    preview JSONB,
    consumed_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investment_confirmations_user ON investment_confirmations (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_investment_confirmations_open ON investment_confirmations (expires_at) WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS investment_glider_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enrollment_id UUID REFERENCES investment_enrollments(id) ON DELETE CASCADE,
    execution_id UUID REFERENCES investment_executions(id) ON DELETE SET NULL,
    provider_operation_id TEXT NOT NULL,
    kind VARCHAR(24) NOT NULL DEFAULT '',
    state VARCHAR(24) NOT NULL DEFAULT 'accepted',
    error TEXT NOT NULL DEFAULT '',
    finished_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (provider_operation_id)
);

CREATE INDEX IF NOT EXISTS idx_investment_operations_open ON investment_glider_operations (state) WHERE state NOT IN ('completed', 'failed', 'cancelled');
CREATE INDEX IF NOT EXISTS idx_investment_operations_user ON investment_glider_operations (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS investment_audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    actor VARCHAR(16) NOT NULL DEFAULT 'system',
    strategy_id UUID,
    strategy_version INTEGER,
    enrollment_id UUID,
    execution_id UUID,
    payload JSONB,
    correlation_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_investment_audit_user ON investment_audit_events (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_investment_audit_type ON investment_audit_events (event_type, created_at DESC);

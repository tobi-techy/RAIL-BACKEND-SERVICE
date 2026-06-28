-- Dedup table for RampHub webhook deliveries. RampHub retries deliveries and
-- sends a unique x-ramphub-delivery id per attempt; we persist it so reprocessing
-- a retried delivery is a cheap no-op (in addition to the business-level
-- idempotency on ramphub_orders).
CREATE TABLE IF NOT EXISTS ramphub_webhook_deliveries (
    delivery_id VARCHAR(255) PRIMARY KEY,
    event_type VARCHAR(64),
    transaction_id VARCHAR(255),
    received_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ramphub_webhook_deliveries_tx ON ramphub_webhook_deliveries(transaction_id);

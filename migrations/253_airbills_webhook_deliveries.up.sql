-- Dedup table for Airbills fulfillment callbacks. Airbills may retry callback
-- deliveries; we persist a delivery id (falling back to a hash of the payload)
-- so reprocessing a retried delivery is a cheap no-op, on top of the
-- business-level idempotency on airbills_orders.
CREATE TABLE IF NOT EXISTS airbills_webhook_deliveries (
    delivery_id VARCHAR(255) PRIMARY KEY,
    product_code VARCHAR(8),
    airbills_id VARCHAR(255),
    received_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_airbills_webhook_deliveries_airbills_id ON airbills_webhook_deliveries(airbills_id);

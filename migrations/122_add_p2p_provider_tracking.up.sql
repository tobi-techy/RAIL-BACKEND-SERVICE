ALTER TABLE p2p_transfers
    ADD COLUMN IF NOT EXISTS provider_transfer_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_status VARCHAR(64);

CREATE INDEX IF NOT EXISTS idx_p2p_transfers_provider_transfer_id
    ON p2p_transfers(provider_transfer_id)
    WHERE provider_transfer_id IS NOT NULL;

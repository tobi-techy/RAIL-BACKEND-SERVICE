DROP INDEX IF EXISTS idx_p2p_transfers_provider_transfer_id;

ALTER TABLE p2p_transfers
    DROP COLUMN IF EXISTS provider_status,
    DROP COLUMN IF EXISTS provider_transfer_id;

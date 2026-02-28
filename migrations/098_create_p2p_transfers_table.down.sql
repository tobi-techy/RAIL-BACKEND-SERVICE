-- Remove P2P transfers tables
DROP TRIGGER IF EXISTS trg_p2p_transfers_updated_at ON p2p_transfers;
DROP FUNCTION IF EXISTS update_p2p_transfers_updated_at();
DROP TABLE IF EXISTS p2p_recent_recipients;
DROP TABLE IF EXISTS p2p_transfers;

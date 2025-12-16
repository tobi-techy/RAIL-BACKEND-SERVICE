-- Drop triggers
DROP TRIGGER IF EXISTS card_transactions_updated_at_trigger ON card_transactions;
DROP TRIGGER IF EXISTS cards_updated_at_trigger ON cards;
DROP FUNCTION IF EXISTS update_cards_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_card_transactions_created_at;
DROP INDEX IF EXISTS idx_card_transactions_bridge_trans_id;
DROP INDEX IF EXISTS idx_card_transactions_user_id;
DROP INDEX IF EXISTS idx_card_transactions_card_id;
DROP INDEX IF EXISTS idx_cards_status;
DROP INDEX IF EXISTS idx_cards_bridge_card_id;
DROP INDEX IF EXISTS idx_cards_user_id;

-- Drop tables
DROP TABLE IF EXISTS card_transactions;
DROP TABLE IF EXISTS cards;

-- Drop transactions table and related objects
DROP TRIGGER IF EXISTS update_transactions_updated_at ON transactions;
DROP FUNCTION IF EXISTS update_updated_at_column();

DROP INDEX IF EXISTS idx_transactions_user_type_status;
DROP INDEX IF EXISTS idx_transactions_user_created;
DROP INDEX IF EXISTS idx_transactions_idempotency_key;
DROP INDEX IF EXISTS idx_transactions_created_at;
DROP INDEX IF EXISTS idx_transactions_currency;
DROP INDEX IF EXISTS idx_transactions_status;
DROP INDEX IF EXISTS idx_transactions_type;
DROP INDEX IF EXISTS idx_transactions_user_id;

DROP TABLE IF EXISTS transactions;

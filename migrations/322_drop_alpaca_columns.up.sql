-- Drop the vestigial Alpaca columns and the Alpaca-only instant funding table.
--
-- The Alpaca brokerage provider has been removed (migrations/321 dropped its
-- tables, and the Go brokerage adapter/entities/vertical are gone). These
-- columns survived only to hold Alpaca identifiers that nothing reads any more;
-- virtual accounts and deposits are Bridge/ChainRails-driven, and instant
-- funding is no longer a product surface.
--
-- withdrawals.alpaca_account_id / alpaca_journal_id were already dropped in 088.

-- users.alpaca_account_id (+ its partial index, dropped with the column)
DROP INDEX IF EXISTS idx_users_alpaca_account_id;
ALTER TABLE users DROP COLUMN IF EXISTS alpaca_account_id;

-- virtual_accounts.alpaca_account_id (+ index; the UNIQUE(user_id, ...) was
-- already dropped in 130)
DROP INDEX IF EXISTS idx_virtual_accounts_alpaca_account_id;
ALTER TABLE virtual_accounts DROP COLUMN IF EXISTS alpaca_account_id;

-- deposits.alpaca_funding_tx_id / alpaca_funded_at (+ index)
DROP INDEX IF EXISTS idx_deposits_alpaca_funding_tx_id;
ALTER TABLE deposits DROP COLUMN IF EXISTS alpaca_funding_tx_id;
ALTER TABLE deposits DROP COLUMN IF EXISTS alpaca_funded_at;

-- instant_fundings existed solely for the Alpaca instant funding journal
DROP TABLE IF EXISTS instant_fundings;

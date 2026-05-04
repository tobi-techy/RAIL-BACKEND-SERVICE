-- Restore the pre-repair system-account uniqueness scope.
-- The revenue account types and seed rows are owned by migration 191.

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational');

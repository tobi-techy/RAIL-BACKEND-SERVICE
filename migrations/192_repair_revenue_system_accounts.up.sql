-- Repair revenue system account setup used by emergency stash withdrawals.
-- Migration 191 introduced these account types; this migration makes the seed
-- state self-healing and extends the system-account uniqueness rule to them.

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_account_type;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_account_type CHECK (account_type IN (
    'usdc_balance', 'spending_balance', 'stash_balance',
    'fiat_exposure', 'pending_investment', 'pending_card_settlement',
    'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
    'subscription_revenue', 'emergency_withdrawal_revenue'
));

INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT uuid_generate_v4(), NULL, 'emergency_withdrawal_revenue', 'USD', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts
    WHERE user_id IS NULL AND account_type = 'emergency_withdrawal_revenue'
);

INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance)
SELECT uuid_generate_v4(), NULL, 'subscription_revenue', 'USD', 0
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts
    WHERE user_id IS NULL AND account_type = 'subscription_revenue'
);

-- Merge duplicate system accounts: reassign entries and sum balances into the canonical row.
DO $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN
        SELECT account_type,
               (array_agg(id ORDER BY created_at, id))[1] AS keep_id,
               array_remove(array_agg(id ORDER BY created_at, id),
                            (array_agg(id ORDER BY created_at, id))[1]) AS dup_ids
        FROM ledger_accounts
        WHERE user_id IS NULL
          AND account_type IN (
              'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
              'subscription_revenue', 'emergency_withdrawal_revenue'
          )
        GROUP BY account_type
        HAVING COUNT(*) > 1
    LOOP
        -- Reassign ledger entries from duplicates to the canonical account
        UPDATE ledger_entries
        SET account_id = rec.keep_id
        WHERE account_id = ANY(rec.dup_ids);

        -- Sum duplicate balances into the canonical account
        UPDATE ledger_accounts
        SET balance = balance + COALESCE((
            SELECT SUM(balance) FROM ledger_accounts WHERE id = ANY(rec.dup_ids)
        ), 0)
        WHERE id = rec.keep_id;

        -- Delete the now-empty duplicates
        DELETE FROM ledger_accounts WHERE id = ANY(rec.dup_ids);
    END LOOP;
END $$;

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'emergency_withdrawal_revenue'
      );

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

WITH duplicate_system_accounts AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY account_type
            ORDER BY
                CASE WHEN balance = 0 THEN 1 ELSE 0 END,
                created_at,
                id
        ) AS row_num
    FROM ledger_accounts
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'emergency_withdrawal_revenue'
      )
),
empty_duplicates AS (
    SELECT d.id
    FROM duplicate_system_accounts d
    WHERE d.row_num > 1
      AND NOT EXISTS (
          SELECT 1 FROM ledger_entries e WHERE e.account_id = d.id
      )
)
DELETE FROM ledger_accounts la
USING empty_duplicates d
WHERE la.id = d.id
  AND la.balance = 0;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM ledger_accounts
        WHERE user_id IS NULL
          AND account_type IN (
              'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
              'subscription_revenue', 'emergency_withdrawal_revenue'
          )
        GROUP BY account_type
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'duplicate system ledger accounts must be resolved before enforcing uniqueness';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_ledger_accounts_system_type;
CREATE UNIQUE INDEX idx_ledger_accounts_system_type
    ON ledger_accounts(account_type)
    WHERE user_id IS NULL
      AND account_type IN (
          'system_buffer_usdc', 'system_buffer_fiat', 'broker_operational',
          'subscription_revenue', 'emergency_withdrawal_revenue'
      );

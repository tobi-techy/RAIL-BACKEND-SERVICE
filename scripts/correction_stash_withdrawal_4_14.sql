-- ============================================================================
-- Manual ledger correction: return $4.14 (stranded stash withdrawal) to SPEND
--
-- Context: a stash withdrawal debited the user's stash_balance but the Blend
-- redemption never settled, so the funds never moved. This credits the user's
-- spending_balance with $4.14 and balances the books against the USDC system
-- buffer (which received the debit side of the original withdrawal).
--
-- Ledger conventions in this schema (see internal/domain/services/ledger):
--   * entry_type 'debit'  => account balance INCREASES
--   * entry_type 'credit' => account balance DECREASES
--   * every transaction must have balanced debits/credits (DB trigger)
--   * ledger_accounts.balance is maintained by the application, so this
--     script updates it explicitly alongside the entries.
--
-- The target account is supplied at run time — no PII is hardcoded:
--   psql -v target_email='user@example.com' -f scripts/correction_stash_withdrawal_4_14.sql
--
-- Run interactively. The COMMIT at the end is intentionally commented out so an
-- operator must review the verification output and issue COMMIT by hand. Run via
-- `-f` and the open transaction rolls back on disconnect (nothing applied).
-- Re-running is safe: the unique idempotency_key + existence guard abort a
-- second application.
-- ============================================================================

\if :{?target_email}
\else
\echo '*** ERROR: pass the account with  -v target_email=user@example.com  ***'
\quit
\endif

-- ── 0. Pre-flight: identify the user and inspect the stranded withdrawal ───
-- Run these SELECTs first and eyeball the results before the DO block.

-- The user:
SELECT id AS user_id, email FROM users WHERE email = :'target_email';

-- Their recent stash-side ledger activity (confirm the $4.14 debit exists and
-- was never reversed):
SELECT lt.id, lt.transaction_type, lt.status, lt.idempotency_key, lt.created_at,
       le.entry_type, le.amount, la.account_type
FROM ledger_transactions lt
JOIN ledger_entries le ON le.transaction_id = lt.id
JOIN ledger_accounts la ON la.id = le.account_id
WHERE lt.user_id = (SELECT id FROM users WHERE email = :'target_email')
  AND la.account_type IN ('stash_balance', 'spending_balance')
  AND lt.created_at > NOW() - INTERVAL '30 days'
ORDER BY lt.created_at DESC
LIMIT 30;

-- Current balances (before):
SELECT account_type, balance
FROM ledger_accounts
WHERE user_id = (SELECT id FROM users WHERE email = :'target_email')
  AND account_type IN ('stash_balance', 'spending_balance');

-- ── 1. The correction ──────────────────────────────────────────────────────

BEGIN;

-- Bridge the psql variable into a transaction-local GUC so the DO block below
-- (a dollar-quoted body, where psql does NOT interpolate :vars) can read it.
SELECT set_config('rail.correction_target_email', :'target_email', true);

DO $$
DECLARE
    v_user_id        UUID;
    v_amount         NUMERIC(36, 18) := 4.14;
    v_idem_key       TEXT := 'manual-correction-stash-withdrawal-2026-07-02';
    v_spend_acct_id  UUID;
    v_buffer_acct_id UUID;
    v_tx_id          UUID := gen_random_uuid();
BEGIN
    SELECT id INTO STRICT v_user_id
    FROM users WHERE email = current_setting('rail.correction_target_email');

    -- User's spending account (must already exist; every funded user has one).
    SELECT id INTO STRICT v_spend_acct_id
    FROM ledger_accounts
    WHERE user_id = v_user_id AND account_type = 'spending_balance';

    -- System USDC buffer (received the debit side of the original withdrawal).
    SELECT id INTO STRICT v_buffer_acct_id
    FROM ledger_accounts
    WHERE user_id IS NULL AND account_type = 'system_buffer_usdc';

    -- Guard 1 (idempotency): abort cleanly if this correction was already applied.
    -- The UNIQUE idempotency_key would also block the INSERT, but a pre-check gives
    -- a clear error before any writes.
    IF EXISTS (SELECT 1 FROM ledger_transactions WHERE idempotency_key = v_idem_key) THEN
        RAISE EXCEPTION 'Correction % already applied — aborting to prevent double credit', v_idem_key;
    END IF;

    -- Guard 2 (existence): the stranded withdrawal must actually exist for this
    -- user. If no stash_balance movement of this amount is on record, refuse —
    -- a blind rerun against the wrong account must not manufacture a credit.
    IF NOT EXISTS (
        SELECT 1
        FROM ledger_entries le
        JOIN ledger_accounts la ON la.id = le.account_id
        WHERE la.user_id = v_user_id
          AND la.account_type = 'stash_balance'
          AND le.amount = v_amount
    ) THEN
        RAISE EXCEPTION 'No stash_balance movement of % found for user % — refusing correction', v_amount, v_user_id;
    END IF;

    -- Transaction header. The UNIQUE idempotency_key makes re-runs abort here too.
    INSERT INTO ledger_transactions
        (id, user_id, transaction_type, status, idempotency_key, description, metadata, created_at, completed_at)
    VALUES
        (v_tx_id, v_user_id, 'reversal', 'completed', v_idem_key,
         'Manual correction: stranded stash withdrawal returned to spending',
         jsonb_build_object(
            'reason', 'stash withdrawal debited but Blend redemption never settled',
            'corrected_by', 'ops-manual',
            'original_flow', 'stash_withdrawal'),
         NOW(), NOW());

    -- Debit user spending (+4.14), credit system buffer (-4.14). Balanced.
    INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
    VALUES
        (gen_random_uuid(), v_tx_id, v_spend_acct_id,  'debit',  v_amount, 'USDC',
         'Manual correction: return stranded stash withdrawal to spending', NOW()),
        (gen_random_uuid(), v_tx_id, v_buffer_acct_id, 'credit', v_amount, 'USDC',
         'Manual correction: counter-entry for stranded stash withdrawal', NOW());

    -- Mirror the entries onto the cached balances (application-maintained).
    UPDATE ledger_accounts SET balance = balance + v_amount, updated_at = NOW()
    WHERE id = v_spend_acct_id;

    UPDATE ledger_accounts SET balance = balance - v_amount, updated_at = NOW()
    WHERE id = v_buffer_acct_id;

    RAISE NOTICE 'Correction applied: tx=% user=% amount=%', v_tx_id, v_user_id, v_amount;
END $$;

-- ── 2. Verify before committing ────────────────────────────────────────────
SELECT account_type, balance
FROM ledger_accounts
WHERE user_id = (SELECT id FROM users WHERE email = :'target_email')
  AND account_type IN ('stash_balance', 'spending_balance');

SELECT lt.id, lt.status, le.entry_type, le.amount, la.account_type
FROM ledger_transactions lt
JOIN ledger_entries le ON le.transaction_id = lt.id
JOIN ledger_accounts la ON la.id = le.account_id
WHERE lt.idempotency_key = 'manual-correction-stash-withdrawal-2026-07-02';

-- ── 3. Commit is a deliberate, separate operator step ───────────────────────
-- Review the two SELECTs above. If the balances look right, type COMMIT; at the
-- psql prompt. If anything looks off, type ROLLBACK; . Do NOT uncomment these —
-- keeping them manual prevents an unattended `-f` run from applying the change.
-- COMMIT;
-- ROLLBACK;

-- ── Notes ───────────────────────────────────────────────────────────────────
-- * The stash-lock DB trigger only guards stash_balance entries; this script
--   touches spending + system buffer only, so it cannot trip it.
-- * If the original withdrawal's Blend redemption row is still non-terminal
--   (SELECT * FROM blend_yield_redemptions WHERE user_id = ... ORDER BY
--   created_at DESC), leave it — if the worker later settles it, real USDC
--   moves from the Blend Safe back into platform custody, which backs the
--   buffer credit made here. Do NOT run this script twice.

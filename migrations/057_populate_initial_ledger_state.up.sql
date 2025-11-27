-- 057_populate_initial_ledger_state.up.sql
-- This migration populates the ledger tables with initial state from existing balances and deposits
-- Strategy:
-- 1. Create system accounts if not exists
-- 2. Create user accounts for all existing balances
-- 3. Create opening balance transactions for each user with non-zero balances
-- 4. Set account balances to match existing balance records

-- ============================================================================
-- STEP 1: Ensure system accounts exist
-- ============================================================================
-- These were auto-created by 056 migration trigger, but we'll ensure they're set up correctly

DO $$
BEGIN
    -- Verify system accounts exist
    IF NOT EXISTS (SELECT 1 FROM ledger_accounts WHERE account_type = 'system_buffer_usdc') THEN
        RAISE EXCEPTION 'System account system_buffer_usdc not found - migration 056 may have failed';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM ledger_accounts WHERE account_type = 'system_buffer_fiat') THEN
        RAISE EXCEPTION 'System account system_buffer_fiat not found - migration 056 may have failed';
    END IF;
    
    IF NOT EXISTS (SELECT 1 FROM ledger_accounts WHERE account_type = 'broker_operational') THEN
        RAISE EXCEPTION 'System account broker_operational not found - migration 056 may have failed';
    END IF;
END $$;

-- ============================================================================
-- STEP 2: Create user accounts for all users with existing balances
-- ============================================================================

-- Create usdc_balance accounts
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
SELECT 
    gen_random_uuid(),
    user_id,
    'usdc_balance',
    'USDC',
    0,  -- Will be set in STEP 4
    COALESCE(updated_at, NOW()),
    COALESCE(updated_at, NOW())
FROM balances
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts la 
    WHERE la.user_id = balances.user_id AND la.account_type = 'usdc_balance'
);

-- Create fiat_exposure accounts
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
SELECT 
    gen_random_uuid(),
    user_id,
    'fiat_exposure',
    'USD',
    0,  -- Will be set in STEP 4
    COALESCE(updated_at, NOW()),
    COALESCE(updated_at, NOW())
FROM balances
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts la 
    WHERE la.user_id = balances.user_id AND la.account_type = 'fiat_exposure'
);

-- Create pending_investment accounts
INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
SELECT 
    gen_random_uuid(),
    user_id,
    'pending_investment',
    'USDC',
    0,  -- Always zero initially
    COALESCE(updated_at, NOW()),
    COALESCE(updated_at, NOW())
FROM balances
WHERE NOT EXISTS (
    SELECT 1 FROM ledger_accounts la 
    WHERE la.user_id = balances.user_id AND la.account_type = 'pending_investment'
);

-- ============================================================================
-- STEP 3: Create opening balance transactions for users with non-zero balances
-- ============================================================================

-- Create transactions for users with buying_power (fiat_exposure)
WITH users_with_buying_power AS (
    SELECT 
        user_id,
        buying_power,
        COALESCE(updated_at, NOW()) as balance_updated_at
    FROM balances
    WHERE buying_power > 0
)
INSERT INTO ledger_transactions (id, transaction_type, idempotency_key, status, description, created_at)
SELECT 
    gen_random_uuid(),
    'conversion',
    'migration_057_buying_power_' || user_id::text,
    'completed',
    'Opening balance migration: Buying power from balances table',
    balance_updated_at
FROM users_with_buying_power;

-- Create entries for buying_power (credit fiat_exposure, debit broker_operational)
WITH buying_power_txns AS (
    SELECT 
        lt.id as transaction_id,
        b.user_id,
        b.buying_power,
        lt.created_at
    FROM ledger_transactions lt
    JOIN balances b ON lt.idempotency_key = 'migration_057_buying_power_' || b.user_id::text
    WHERE lt.transaction_type = 'conversion'
      AND b.buying_power > 0
),
user_fiat_accounts AS (
    SELECT la.id as account_id, la.user_id
    FROM ledger_accounts la
    WHERE la.account_type = 'fiat_exposure'
),
broker_account AS (
    SELECT id as account_id
    FROM ledger_accounts
    WHERE account_type = 'broker_operational'
    LIMIT 1
)
-- Credit user's fiat_exposure account
INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, created_at)
SELECT 
    gen_random_uuid(),
    t.transaction_id,
    a.account_id,
    'credit',
    t.buying_power,
    t.created_at
FROM buying_power_txns t
JOIN user_fiat_accounts a ON t.user_id = a.user_id

UNION ALL

-- Debit broker_operational account
SELECT 
    gen_random_uuid(),
    t.transaction_id,
    (SELECT account_id FROM broker_account),
    'debit',
    t.buying_power,
    t.created_at
FROM buying_power_txns t;

-- Create transactions for users with pending_deposits (still on-chain as USDC)
WITH users_with_pending AS (
    SELECT 
        user_id,
        pending_deposits,
        COALESCE(updated_at, NOW()) as balance_updated_at
    FROM balances
    WHERE pending_deposits > 0
)
INSERT INTO ledger_transactions (id, transaction_type, idempotency_key, status, description, created_at)
SELECT 
    gen_random_uuid(),
    'deposit',
    'migration_057_pending_' || user_id::text,
    'pending',
    'Opening balance migration: Pending deposits from balances table',
    balance_updated_at
FROM users_with_pending;

-- Create entries for pending_deposits (credit usdc_balance, debit system_buffer_usdc)
WITH pending_txns AS (
    SELECT 
        lt.id as transaction_id,
        b.user_id,
        b.pending_deposits,
        lt.created_at
    FROM ledger_transactions lt
    JOIN balances b ON lt.idempotency_key = 'migration_057_pending_' || b.user_id::text
    WHERE lt.transaction_type = 'deposit'
      AND b.pending_deposits > 0
),
user_usdc_accounts AS (
    SELECT la.id as account_id, la.user_id
    FROM ledger_accounts la
    WHERE la.account_type = 'usdc_balance'
),
system_usdc_account AS (
    SELECT id as account_id
    FROM ledger_accounts
    WHERE account_type = 'system_buffer_usdc'
    LIMIT 1
)
-- Credit user's usdc_balance account
INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, created_at)
SELECT 
    gen_random_uuid(),
    t.transaction_id,
    a.account_id,
    'credit',
    t.pending_deposits,
    t.created_at
FROM pending_txns t
JOIN user_usdc_accounts a ON t.user_id = a.user_id

UNION ALL

-- Debit system_buffer_usdc account
SELECT 
    gen_random_uuid(),
    t.transaction_id,
    (SELECT account_id FROM system_usdc_account),
    'debit',
    t.pending_deposits,
    t.created_at
FROM pending_txns t;

-- ============================================================================
-- STEP 4: Update account balances to match ledger entries
-- ============================================================================

-- Update user usdc_balance accounts
UPDATE ledger_accounts la
SET 
    balance = COALESCE(
        (SELECT SUM(
            CASE 
                WHEN le.entry_type = 'credit' THEN le.amount
                WHEN le.entry_type = 'debit' THEN -le.amount
            END
        )
        FROM ledger_entries le
        WHERE le.account_id = la.id),
        0
    ),
    updated_at = NOW()
WHERE la.account_type = 'usdc_balance';

-- Update user fiat_exposure accounts
UPDATE ledger_accounts la
SET 
    balance = COALESCE(
        (SELECT SUM(
            CASE 
                WHEN le.entry_type = 'credit' THEN le.amount
                WHEN le.entry_type = 'debit' THEN -le.amount
            END
        )
        FROM ledger_entries le
        WHERE le.account_id = la.id),
        0
    ),
    updated_at = NOW()
WHERE la.account_type = 'fiat_exposure';

-- Update system accounts
UPDATE ledger_accounts la
SET 
    balance = COALESCE(
        (SELECT SUM(
            CASE 
                WHEN le.entry_type = 'credit' THEN le.amount
                WHEN le.entry_type = 'debit' THEN -le.amount
            END
        )
        FROM ledger_entries le
        WHERE le.account_id = la.id),
        0
    ),
    updated_at = NOW()
WHERE la.account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational');

-- ============================================================================
-- STEP 5: Validation - Ensure ledger balances match original balances table
-- ============================================================================

DO $$
DECLARE
    mismatch_count integer;
    total_users integer;
    usdc_discrepancy numeric;
    fiat_discrepancy numeric;
BEGIN
    -- Check user account count
    SELECT COUNT(DISTINCT user_id) INTO total_users FROM balances;
    
    -- Check if all users have ledger accounts
    IF (SELECT COUNT(DISTINCT user_id) FROM ledger_accounts WHERE user_id IS NOT NULL) < total_users THEN
        RAISE WARNING 'Not all users from balances table have ledger accounts';
    END IF;
    
    -- Check for balance mismatches in fiat_exposure
    SELECT COUNT(*) INTO mismatch_count
    FROM balances b
    JOIN ledger_accounts la ON b.user_id = la.user_id AND la.account_type = 'fiat_exposure'
    WHERE ABS(b.buying_power - la.balance) > 0.01;  -- Allow 1 cent tolerance for rounding
    
    IF mismatch_count > 0 THEN
        RAISE WARNING 'Found % users with fiat_exposure balance mismatches', mismatch_count;
        
        -- Log details of mismatches
        SELECT 
            SUM(b.buying_power - la.balance) INTO fiat_discrepancy
        FROM balances b
        JOIN ledger_accounts la ON b.user_id = la.user_id AND la.account_type = 'fiat_exposure'
        WHERE ABS(b.buying_power - la.balance) > 0.01;
        
        RAISE WARNING 'Total fiat discrepancy: %', fiat_discrepancy;
    END IF;
    
    -- Check for balance mismatches in usdc_balance
    SELECT COUNT(*) INTO mismatch_count
    FROM balances b
    JOIN ledger_accounts la ON b.user_id = la.user_id AND la.account_type = 'usdc_balance'
    WHERE ABS(b.pending_deposits - la.balance) > 0.01;
    
    IF mismatch_count > 0 THEN
        RAISE WARNING 'Found % users with usdc_balance balance mismatches', mismatch_count;
        
        SELECT 
            SUM(b.pending_deposits - la.balance) INTO usdc_discrepancy
        FROM balances b
        JOIN ledger_accounts la ON b.user_id = la.user_id AND la.account_type = 'usdc_balance'
        WHERE ABS(b.pending_deposits - la.balance) > 0.01;
        
        RAISE WARNING 'Total USDC discrepancy: %', usdc_discrepancy;
    END IF;
    
    -- Verify double-entry invariant
    IF EXISTS (
        SELECT 1
        FROM ledger_transactions lt
        LEFT JOIN (
            SELECT 
                transaction_id,
                SUM(CASE WHEN entry_type = 'debit' THEN amount ELSE 0 END) as total_debits,
                SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE 0 END) as total_credits
            FROM ledger_entries
            GROUP BY transaction_id
        ) e ON lt.id = e.transaction_id
        WHERE ABS(COALESCE(e.total_debits, 0) - COALESCE(e.total_credits, 0)) > 0.01
    ) THEN
        RAISE EXCEPTION 'Double-entry validation failed: debits do not equal credits';
    END IF;
    
    RAISE NOTICE 'Ledger migration completed successfully for % users', total_users;
END $$;

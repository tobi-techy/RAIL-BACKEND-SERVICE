-- mono_linked_accounts: bank accounts linked through Mono Connect.
-- The mono_account_id is the persistent identifier from POST /v2/accounts/auth
-- and is used for all subsequent Financial Data API calls (transactions, income, etc.).
--
-- DirectPay one-time debits reference the linked account to pull funds from the
-- user's bank account into Rail's collection account.

CREATE TABLE mono_linked_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mono_account_id TEXT NOT NULL UNIQUE,          -- Mono's persistent account ID
    institution TEXT NOT NULL DEFAULT '',           -- bank name
    account_name TEXT NOT NULL DEFAULT '',          -- account holder name
    account_number TEXT NOT NULL DEFAULT '',        -- last 4 digits for display
    account_type TEXT NOT NULL DEFAULT '',          -- savings, current, etc.
    currency TEXT NOT NULL DEFAULT 'NGN',           -- NGN, GHS, KES, ZAR
    balance BIGINT NOT NULL DEFAULT 0,              -- in kobo/pesewa/cents
    status TEXT NOT NULL DEFAULT 'linked',          -- linked, reauth, unlinked
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One linked account per user per mono_account_id (prevents duplicate linking).
CREATE UNIQUE INDEX mono_linked_accounts_user_mono_idx
    ON mono_linked_accounts(user_id, mono_account_id);

-- Active accounts for a user (hot path: listing + sync).
CREATE INDEX mono_linked_accounts_user_active_idx
    ON mono_linked_accounts(user_id) WHERE status = 'linked';

-- mono_imported_transactions: transactions synced from Mono-linked accounts.
-- Amounts are stored in kobo/pesewa/cents (the raw Mono unit).
CREATE TABLE mono_imported_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES mono_linked_accounts(id) ON DELETE CASCADE,
    mono_txn_id TEXT NOT NULL,                      -- Mono's transaction _id
    amount BIGINT NOT NULL,                         -- kobo/pesewa/cents
    type TEXT NOT NULL CHECK (type IN ('credit', 'debit')),
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    sub_category TEXT NOT NULL DEFAULT '',
    transaction_date TIMESTAMPTZ NOT NULL,
    reference TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Deduplicate on Mono transaction ID per account.
CREATE UNIQUE INDEX mono_imported_transactions_account_txn_idx
    ON mono_imported_transactions(account_id, mono_txn_id);

-- Spending analysis hot path: recent transactions for a user.
CREATE INDEX mono_imported_transactions_user_date_idx
    ON mono_imported_transactions(user_id, transaction_date DESC);

-- Category breakdown queries.
CREATE INDEX mono_imported_transactions_user_type_cat_idx
    ON mono_imported_transactions(user_id, type, category)
    WHERE type = 'debit';

-- mono_payments: DirectPay one-time payments initiated through Mono.
CREATE TABLE mono_payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES mono_linked_accounts(id) ON DELETE CASCADE,
    amount BIGINT NOT NULL,                         -- kobo
    reference TEXT NOT NULL UNIQUE,                 -- merchant-unique reference
    status TEXT NOT NULL DEFAULT 'pending',         -- pending, successful, failed, reversed
    mono_ref TEXT NOT NULL DEFAULT '',              -- Mono's internal reference
    approval_url TEXT NOT NULL DEFAULT '',          -- URL the user visits to authorise
    description TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Payment lookup by reference (verify endpoint).
CREATE INDEX mono_payments_reference_idx
    ON mono_payments(reference);

-- User's payment history.
CREATE INDEX mono_payments_user_date_idx
    ON mono_payments(user_id, created_at DESC);

-- Pending payments for polling/verification workers.
CREATE INDEX mono_payments_pending_idx
    ON mono_payments(status, created_at) WHERE status = 'pending';

COMMENT ON TABLE mono_linked_accounts IS 'Bank accounts linked through Mono Connect. mono_account_id is the persistent ID from the exchange-token endpoint.';
COMMENT ON TABLE mono_imported_transactions IS 'Transactions synced from Mono-linked accounts. Amounts in kobo/pesewa/cents.';
COMMENT ON TABLE mono_payments IS 'DirectPay one-time payments from Mono-linked bank accounts. Verify status before giving value.';

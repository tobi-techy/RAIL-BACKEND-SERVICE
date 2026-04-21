-- Enforce stash lock at the database level.
-- Prevents debits from stash_balance accounts when the user has an active lock cycle.

CREATE OR REPLACE FUNCTION enforce_stash_lock()
RETURNS TRIGGER AS $$
DECLARE
    v_account_type TEXT;
    v_user_id UUID;
    v_has_lock BOOLEAN;
BEGIN
    -- Only check debits
    IF NEW.entry_type != 'debit' THEN
        RETURN NEW;
    END IF;

    -- Look up the account type and user
    SELECT account_type, user_id INTO v_account_type, v_user_id
    FROM ledger_accounts WHERE id = NEW.account_id;

    -- Only enforce on stash_balance accounts
    IF v_account_type != 'stash_balance' OR v_user_id IS NULL THEN
        RETURN NEW;
    END IF;

    -- Check for active lock
    SELECT EXISTS (
        SELECT 1 FROM stash_lock_cycles
        WHERE user_id = v_user_id AND status = 'locked'
    ) INTO v_has_lock;

    IF v_has_lock THEN
        RAISE EXCEPTION 'stash_lock_violation: user % has an active stash lock', v_user_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_stash_lock
    BEFORE INSERT ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION enforce_stash_lock();
